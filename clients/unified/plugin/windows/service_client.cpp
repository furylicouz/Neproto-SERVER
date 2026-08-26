#include "service_client.h"

#include <algorithm>
#include <cctype>
#include <initializer_list>
#include <set>
#include <string_view>
#include <utility>

namespace neproto_host {
namespace {

class PipeServiceTransport final : public ServiceTransport {
 public:
  PipeServiceTransport() : client_(CreateNamedPipeConnector()) {}

  PipeResponse Transact(const std::string& request) override {
    return client_.Transact(request);
  }

 private:
  PipeClient client_;
};

ServiceResult Failure(std::string code, std::string stage,
                      std::string message, bool retryable,
                      std::string operation_id) {
  return {std::nullopt,
          ServiceError{std::move(code), std::move(stage), std::move(message),
                       retryable, std::move(operation_id)}};
}

ServiceResult TransportFailure(PipeError error) {
  switch (error) {
    case PipeError::kHostUnavailable:
    case PipeError::kDeadlineExceeded:
    case PipeError::kIoFailure:
      return Failure("HOST_UNAVAILABLE", "HOST_IPC",
                     "Native host is unavailable.", true, "transport");
    case PipeError::kMalformedFrame:
    case PipeError::kMessageTooLarge:
      return Failure("INTERNAL", "HOST_IPC",
                     "Native host response was rejected.", false,
                     "transport");
    case PipeError::kNone:
      break;
  }
  return Failure("INTERNAL", "HOST_IPC", "Native host command failed.",
                 false, "transport");
}

bool HasOnlyKeys(const JsonValue::Object& object,
                 std::initializer_list<std::string_view> allowed) {
  for (const auto& [key, value] : object) {
    static_cast<void>(value);
    if (std::find(allowed.begin(), allowed.end(), key) == allowed.end()) {
      return false;
    }
  }
  return true;
}

bool PrintableOperationId(const std::string& value) {
  if (value.empty() || value.size() > 64) {
    return false;
  }
  return std::all_of(value.begin(), value.end(), [](unsigned char character) {
    return character >= 0x21 && character <= 0x7e;
  });
}

bool SafeMessage(const std::string& value) {
  if (value.empty() || value.size() > 512 || value.front() == ' ' ||
      value.back() == ' ' ||
      std::any_of(value.begin(), value.end(), [](unsigned char character) {
        return character < 0x20 || character == 0x7f;
      })) {
    return false;
  }
  std::string lower = value;
  std::transform(lower.begin(), lower.end(), lower.begin(),
                 [](unsigned char character) {
                   return static_cast<char>(std::tolower(character));
                 });
  return lower.find("np2://") == std::string::npos;
}

bool ValidCode(const std::string& value) {
  static const std::set<std::string, std::less<>> kValues{
      "HOST_UNAVAILABLE",       "UNSUPPORTED_API_VERSION",
      "INVALID_PROFILE",        "CREDENTIAL_UNAVAILABLE",
      "NO_SAFE_UPLINK",         "DNS_FAILED",
      "UDP_UNREACHABLE",        "TLS_FAILED",
      "HTTP3_TIMEOUT",          "NP2_AUTH_FAILED",
      "TUN_SETUP_FAILED",       "CANCELLED",
      "INTERNAL"};
  return kValues.find(value) != kValues.end();
}

bool ValidStage(const std::string& value) {
  static const std::set<std::string, std::less<>> kValues{
      "UNKNOWN",              "HOST_IPC",
      "HOST_NEGOTIATION",     "PROFILE_VALIDATION",
      "CREDENTIAL_LOAD",      "DNS_RESOLUTION",
      "ENDPOINT_ROUTE",       "QUIC_HANDSHAKE",
      "TLS_HANDSHAKE",        "WEBTRANSPORT_CONNECT",
      "NP2_AUTHENTICATION",   "TUN_SETUP",
      "PACKET_FORWARDING"};
  return kValues.find(value) != kValues.end();
}

std::optional<ServiceError> ParseServiceError(const JsonValue& value) {
  const JsonValue::Object* object = value.object();
  if (object == nullptr || object->size() != 5 ||
      !HasOnlyKeys(*object,
                   {"code", "stage", "message", "retryable",
                    "operation_id"})) {
    return std::nullopt;
  }
  const JsonValue* code = value.Find("code");
  const JsonValue* stage = value.Find("stage");
  const JsonValue* message = value.Find("message");
  const JsonValue* retryable = value.Find("retryable");
  const JsonValue* operation_id = value.Find("operation_id");
  if (code == nullptr || stage == nullptr || message == nullptr ||
      retryable == nullptr || operation_id == nullptr ||
      code->string() == nullptr || stage->string() == nullptr ||
      message->string() == nullptr || retryable->boolean() == nullptr ||
      operation_id->string() == nullptr || !ValidCode(*code->string()) ||
      !ValidStage(*stage->string()) || !SafeMessage(*message->string()) ||
      !PrintableOperationId(*operation_id->string())) {
    return std::nullopt;
  }
  return ServiceError{*code->string(), *stage->string(), *message->string(),
                      *retryable->boolean(), *operation_id->string()};
}

ServiceResult ParseEnvelope(const std::string& payload,
                            const std::string& expected_id) {
  auto parsed = ParseJson(payload);
  if (!parsed) {
    return TransportFailure(PipeError::kMalformedFrame);
  }
  const JsonValue::Object* object = parsed->object();
  if (object == nullptr ||
      !HasOnlyKeys(*object, {"id", "ok", "result", "error", "host_error"})) {
    return TransportFailure(PipeError::kMalformedFrame);
  }
  const JsonValue* id = parsed->Find("id");
  const JsonValue* ok = parsed->Find("ok");
  if (id == nullptr || ok == nullptr || id->string() == nullptr ||
      ok->boolean() == nullptr || *id->string() != expected_id) {
    return TransportFailure(PipeError::kMalformedFrame);
  }
  if (*ok->boolean()) {
    const JsonValue* result = parsed->Find("result");
    if (result == nullptr || parsed->Find("error") != nullptr ||
        parsed->Find("host_error") != nullptr) {
      return TransportFailure(PipeError::kMalformedFrame);
    }
    return {JsonValue(*result), std::nullopt};
  }
  const JsonValue* error_text = parsed->Find("error");
  const JsonValue* host_error = parsed->Find("host_error");
  if (parsed->Find("result") != nullptr || error_text == nullptr ||
      error_text->string() == nullptr || host_error == nullptr) {
    return TransportFailure(PipeError::kMalformedFrame);
  }
  auto error = ParseServiceError(*host_error);
  if (!error || error->message != *error_text->string()) {
    return TransportFailure(PipeError::kMalformedFrame);
  }
  return {std::nullopt, std::move(error)};
}

bool ValidHostMethod(const std::string& method) {
  static const std::set<std::string, std::less<>> kMethods{
      "host.v1.capabilities",      "host.v1.profiles.list",
      "host.v1.profiles.import",   "host.v1.profiles.select",
      "host.v1.profiles.remove",   "host.v1.tunnel.connect",
      "host.v1.tunnel.disconnect", "host.v1.tunnel.status",
      "host.v1.diagnostics.get"};
  return kMethods.find(method) != kMethods.end();
}

}  // namespace

ServiceClient::ServiceClient(std::unique_ptr<ServiceTransport> transport)
    : transport_(std::move(transport)) {}

ServiceResult ServiceClient::Call(const std::string& method,
                                  const std::string& params_json) {
  auto params = ParseJson(params_json);
  if (!transport_ || !ValidHostMethod(method) || !params ||
      params->object() == nullptr) {
    return Failure("INTERNAL", "HOST_IPC", "Native host request was rejected.",
                   false, "request");
  }
  const std::string id = "pigeon-" + std::to_string(sequence_.fetch_add(1) + 1);
  const auto quoted_id = JsonQuoted(id);
  const auto quoted_method = JsonQuoted(method);
  if (!quoted_id || !quoted_method) {
    return Failure("INTERNAL", "HOST_IPC", "Native host request was rejected.",
                   false, "request");
  }
  const std::string request = "{\"id\":" + *quoted_id + ",\"method\":" +
                              *quoted_method + ",\"params\":" + params_json +
                              "}";
  if (request.size() > kMaxIpcMessageBytes) {
    return TransportFailure(PipeError::kMessageTooLarge);
  }
  const PipeResponse response = transport_->Transact(request);
  if (!response.ok()) {
    return TransportFailure(response.error);
  }
  return ParseEnvelope(response.payload, id);
}

std::unique_ptr<ServiceTransport> CreatePipeServiceTransport() {
  return std::make_unique<PipeServiceTransport>();
}

}  // namespace neproto_host
