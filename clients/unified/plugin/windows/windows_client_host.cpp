#include "windows_client_host.h"

#include <flutter/encodable_value.h>

#include <algorithm>
#include <cctype>
#include <cstdint>
#include <initializer_list>
#include <optional>
#include <string>
#include <string_view>
#include <utility>

namespace neproto_host {
namespace {

constexpr std::int64_t kHostApiMajor = 1;
constexpr std::int64_t kHostApiMinor = 0;

bool OnlyKeys(const JsonValue& value,
              std::initializer_list<std::string_view> allowed) {
  const JsonValue::Object* object = value.object();
  if (object == nullptr) {
    return false;
  }
  for (const auto& [key, child] : *object) {
    static_cast<void>(child);
    if (std::find(allowed.begin(), allowed.end(), key) == allowed.end()) {
      return false;
    }
  }
  return true;
}

const std::string* StringField(const JsonValue& value, std::string_view key) {
  const JsonValue* field = value.Find(key);
  return field == nullptr ? nullptr : field->string();
}

const std::int64_t* IntegerField(const JsonValue& value,
                                 std::string_view key) {
  const JsonValue* field = value.Find(key);
  return field == nullptr ? nullptr : field->integer();
}

const bool* BooleanField(const JsonValue& value, std::string_view key) {
  const JsonValue* field = value.Find(key);
  return field == nullptr ? nullptr : field->boolean();
}

bool SafeText(const std::string& value, std::size_t maximum,
              bool allow_empty = false) {
  if ((!allow_empty && value.empty()) || value.size() > maximum ||
      (!value.empty() && (value.front() == ' ' || value.back() == ' ')) ||
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

std::size_t Utf8ScalarCount(const std::string& value) {
  return static_cast<std::size_t>(std::count_if(
      value.begin(), value.end(), [](unsigned char character) {
        return (character & 0xc0) != 0x80;
      }));
}

bool ValidOperationId(const std::string& value) {
  return value.size() >= 1 && value.size() <= 64 &&
         std::all_of(value.begin(), value.end(), [](unsigned char character) {
           return character >= 0x21 && character <= 0x7e;
         });
}

bool ValidProfileId(const std::string& value) {
  return SafeText(value, 128) && value.front() != ' ' && value.back() != ' ' &&
         std::none_of(value.begin(), value.end(), [](unsigned char character) {
           return character < 0x20 || character == 0x7f;
         });
}

bool ValidOnboarding(const std::string& value) {
  return value.size() <= 16 * 1024 && value.size() > 16 &&
         value.front() != ' ' && value.back() != ' ' &&
         (value.rfind("np2://import/v1/", 0) == 0 ||
          value.rfind("np2://import/v2/", 0) == 0) &&
         JsonQuoted(value).has_value();
}

FlutterError LocalError(std::string code, std::string stage,
                        std::string message, bool retryable,
                        std::string operation_id) {
  ::flutter::EncodableMap details;
  details[::flutter::EncodableValue("code")] =
      ::flutter::EncodableValue(code);
  details[::flutter::EncodableValue("stage")] =
      ::flutter::EncodableValue(stage);
  details[::flutter::EncodableValue("retryable")] =
      ::flutter::EncodableValue(retryable);
  details[::flutter::EncodableValue("operationId")] =
      ::flutter::EncodableValue(operation_id);
  return FlutterError(code, message, ::flutter::EncodableValue(details));
}

FlutterError ToFlutterError(const std::optional<ServiceError>& error) {
  if (!error) {
    return LocalError("INTERNAL", "HOST_IPC", "Native host command failed.",
                      false, "request");
  }
  return LocalError(error->code, error->stage, error->message,
                    error->retryable, error->operation_id);
}

FlutterError InvalidResponse() {
  return LocalError("INTERNAL", "HOST_IPC",
                    "Native host response was rejected.", false, "response");
}

std::optional<HostPlatform> ParsePlatform(const std::string& value) {
  if (value == "windows") return HostPlatform::kWindows;
  if (value == "ios") return HostPlatform::kIos;
  if (value == "unknown") return HostPlatform::kUnknown;
  return std::nullopt;
}

std::optional<ProfileOrigin> ParseProfileOrigin(const std::string& value) {
  if (value == "imported") return ProfileOrigin::kImported;
  if (value == "cluster") return ProfileOrigin::kCluster;
  if (value == "unknown") return ProfileOrigin::kUnknown;
  return std::nullopt;
}

std::optional<TunnelState> ParseTunnelState(const std::string& value) {
  if (value == "disconnected") return TunnelState::kDisconnected;
  if (value == "connecting") return TunnelState::kConnecting;
  if (value == "connected") return TunnelState::kConnected;
  if (value == "reconnecting") return TunnelState::kReconnecting;
  if (value == "disconnecting") return TunnelState::kDisconnecting;
  if (value == "failed") return TunnelState::kFailed;
  if (value == "unknown") return TunnelState::kUnknown;
  return std::nullopt;
}

std::optional<CarrierKind> ParseCarrier(const std::string& value) {
  if (value == "none") return CarrierKind::kNone;
  if (value == "http3_webtransport") return CarrierKind::kHttp3WebTransport;
  if (value == "unknown") return CarrierKind::kUnknown;
  return std::nullopt;
}

std::optional<HostErrorCode> ParseErrorCode(const std::string& value) {
  if (value == "HOST_UNAVAILABLE") return HostErrorCode::kHostUnavailable;
  if (value == "UNSUPPORTED_API_VERSION") return HostErrorCode::kUnsupportedApiVersion;
  if (value == "INVALID_PROFILE") return HostErrorCode::kInvalidProfile;
  if (value == "CREDENTIAL_UNAVAILABLE") return HostErrorCode::kCredentialUnavailable;
  if (value == "NO_SAFE_UPLINK") return HostErrorCode::kNoSafeUplink;
  if (value == "DNS_FAILED") return HostErrorCode::kDnsFailed;
  if (value == "UDP_UNREACHABLE") return HostErrorCode::kUdpUnreachable;
  if (value == "TLS_FAILED") return HostErrorCode::kTlsFailed;
  if (value == "HTTP3_TIMEOUT") return HostErrorCode::kHttp3Timeout;
  if (value == "NP2_AUTH_FAILED") return HostErrorCode::kNp2AuthFailed;
  if (value == "TUN_SETUP_FAILED") return HostErrorCode::kTunSetupFailed;
  if (value == "CANCELLED") return HostErrorCode::kCancelled;
  if (value == "INTERNAL") return HostErrorCode::kInternal;
  if (value == "UNKNOWN") return HostErrorCode::kUnknown;
  return std::nullopt;
}

std::optional<ErrorStage> ParseErrorStage(const std::string& value) {
  if (value == "HOST_IPC") return ErrorStage::kHostIpc;
  if (value == "HOST_NEGOTIATION") return ErrorStage::kHostNegotiation;
  if (value == "PROFILE_VALIDATION") return ErrorStage::kProfileValidation;
  if (value == "CREDENTIAL_LOAD") return ErrorStage::kCredentialLoad;
  if (value == "DNS_RESOLUTION") return ErrorStage::kDnsResolution;
  if (value == "ENDPOINT_ROUTE") return ErrorStage::kEndpointRoute;
  if (value == "QUIC_HANDSHAKE") return ErrorStage::kQuicHandshake;
  if (value == "TLS_HANDSHAKE") return ErrorStage::kTlsHandshake;
  if (value == "WEBTRANSPORT_CONNECT") return ErrorStage::kWebTransportConnect;
  if (value == "NP2_AUTHENTICATION") return ErrorStage::kNp2Authentication;
  if (value == "TUN_SETUP") return ErrorStage::kTunSetup;
  if (value == "PACKET_FORWARDING") return ErrorStage::kPacketForwarding;
  if (value == "UNKNOWN") return ErrorStage::kUnknown;
  return std::nullopt;
}

std::optional<DiagnosticLevel> ParseDiagnosticLevel(const std::string& value) {
  if (value == "info") return DiagnosticLevel::kInfo;
  if (value == "warning") return DiagnosticLevel::kWarning;
  if (value == "error") return DiagnosticLevel::kError;
  if (value == "unknown") return DiagnosticLevel::kUnknown;
  return std::nullopt;
}

std::optional<HostError> ParseHostError(const JsonValue& value) {
  if (!OnlyKeys(value,
                {"code", "stage", "message", "retryable", "operation_id"}) ||
      value.object()->size() != 5) {
    return std::nullopt;
  }
  const auto* code_value = StringField(value, "code");
  const auto* stage_value = StringField(value, "stage");
  const auto* message = StringField(value, "message");
  const auto* retryable = BooleanField(value, "retryable");
  const auto* operation_id = StringField(value, "operation_id");
  if (!code_value || !stage_value || !message || !retryable || !operation_id) {
    return std::nullopt;
  }
  const auto code = ParseErrorCode(*code_value);
  const auto stage = ParseErrorStage(*stage_value);
  if (!code || !stage || !SafeText(*message, 512) ||
      !ValidOperationId(*operation_id)) {
    return std::nullopt;
  }
  return HostError(*code, *stage, *message, *retryable, *operation_id);
}

std::optional<HostCapabilities> ParseCapabilities(const JsonValue& value) {
  if (!OnlyKeys(value,
                {"api_version", "platform", "app_version", "host_version",
                 "core_version", "supports_http3_web_transport"}) ||
      value.object()->size() != 6) {
    return std::nullopt;
  }
  const JsonValue* version = value.Find("api_version");
  const auto* platform_value = StringField(value, "platform");
  const auto* app_version = StringField(value, "app_version");
  const auto* host_version = StringField(value, "host_version");
  const auto* core_version = StringField(value, "core_version");
  const auto* supports = BooleanField(value, "supports_http3_web_transport");
  if (!version || !OnlyKeys(*version, {"major", "minor"}) ||
      version->object()->size() != 2 || !platform_value || !app_version ||
      !host_version || !core_version || !supports) {
    return std::nullopt;
  }
  const auto* major = IntegerField(*version, "major");
  const auto* minor = IntegerField(*version, "minor");
  const auto platform = ParsePlatform(*platform_value);
  if (!major || !minor || *major < 0 || *minor < 0 || !platform ||
      *platform != HostPlatform::kWindows || !SafeText(*app_version, 64) ||
      !SafeText(*host_version, 64) || !SafeText(*core_version, 64) ||
      !*supports) {
    return std::nullopt;
  }
  return HostCapabilities(HostApiVersion(*major, *minor), *platform,
                          *app_version, *host_version, *core_version, *supports);
}

std::optional<ProfileSummary> ParseProfile(const JsonValue& value) {
  if (!OnlyKeys(value,
                {"id", "display_name", "server_identity", "host", "selected",
                 "has_credential", "origin", "catalog_managed",
                 "updated_at_unix_ms"}) ||
      value.object()->size() != 9) {
    return std::nullopt;
  }
  const auto* id = StringField(value, "id");
  const auto* display_name = StringField(value, "display_name");
  const auto* identity = StringField(value, "server_identity");
  const auto* host = StringField(value, "host");
  const auto* selected = BooleanField(value, "selected");
  const auto* credential = BooleanField(value, "has_credential");
  const auto* origin_value = StringField(value, "origin");
  const auto* managed = BooleanField(value, "catalog_managed");
  const auto* updated = IntegerField(value, "updated_at_unix_ms");
  if (!id || !display_name || !identity || !host || !selected || !credential ||
      !origin_value || !managed || !updated) {
    return std::nullopt;
  }
  const auto origin = ParseProfileOrigin(*origin_value);
  if (!origin || *origin == ProfileOrigin::kUnknown || !ValidProfileId(*id) ||
      !SafeText(*display_name, 512) || Utf8ScalarCount(*display_name) > 128 ||
      !SafeText(*identity, 255) ||
      !SafeText(*host, 255) || *updated < 0) {
    return std::nullopt;
  }
  return ProfileSummary(*id, *display_name, *identity, *host, *selected,
                        *credential, *origin, *managed, *updated);
}

std::optional<TunnelStatus> ParseStatus(const JsonValue& value) {
  if (!OnlyKeys(value,
                {"state", "profile_id", "carrier", "connected_at_unix_ms",
                 "upload_bytes_per_second", "download_bytes_per_second",
                 "upload_total_bytes", "download_total_bytes", "sequence",
                 "last_error"})) {
    return std::nullopt;
  }
  const auto* state_value = StringField(value, "state");
  const auto* carrier_value = StringField(value, "carrier");
  const auto* connected_at = IntegerField(value, "connected_at_unix_ms");
  const auto* upload_rate = IntegerField(value, "upload_bytes_per_second");
  const auto* download_rate = IntegerField(value, "download_bytes_per_second");
  const auto* upload_total = IntegerField(value, "upload_total_bytes");
  const auto* download_total = IntegerField(value, "download_total_bytes");
  const auto* sequence = IntegerField(value, "sequence");
  if (!state_value || !carrier_value || !connected_at || !upload_rate ||
      !download_rate || !upload_total || !download_total || !sequence) {
    return std::nullopt;
  }
  const auto state = ParseTunnelState(*state_value);
  const auto carrier = ParseCarrier(*carrier_value);
  if (!state || !carrier || *state == TunnelState::kUnknown ||
      *connected_at < 0 || *upload_rate < 0 || *download_rate < 0 ||
      *upload_total < 0 || *download_total < 0 || *sequence < 0 ||
      ((*state == TunnelState::kConnected ||
        *state == TunnelState::kReconnecting) &&
       *carrier != CarrierKind::kHttp3WebTransport) ||
      (*state == TunnelState::kDisconnected && *carrier != CarrierKind::kNone)) {
    return std::nullopt;
  }
  const std::string* profile_id = nullptr;
  if (const JsonValue* field = value.Find("profile_id")) {
    profile_id = field->string();
    if (!profile_id || !ValidProfileId(*profile_id)) return std::nullopt;
  }
  std::optional<HostError> last_error;
  if (const JsonValue* field = value.Find("last_error")) {
    last_error = ParseHostError(*field);
    if (!last_error) return std::nullopt;
  }
  return TunnelStatus(*state, profile_id, *carrier, *connected_at, *upload_rate,
                      *download_rate, *upload_total, *download_total, *sequence,
                      last_error ? &*last_error : nullptr);
}

std::optional<DiagnosticEvent> ParseDiagnosticEvent(const JsonValue& value) {
  if (!OnlyKeys(value,
                {"unix_ms", "level", "stage", "code", "message",
                 "operation_id", "sequence"})) {
    return std::nullopt;
  }
  const auto* unix_ms = IntegerField(value, "unix_ms");
  const auto* level_value = StringField(value, "level");
  const auto* stage_value = StringField(value, "stage");
  const auto* message = StringField(value, "message");
  const auto* operation_id = StringField(value, "operation_id");
  const auto* sequence = IntegerField(value, "sequence");
  if (!unix_ms || !level_value || !stage_value || !message || !operation_id ||
      !sequence || *unix_ms < 0 || *sequence < 0 || !SafeText(*message, 512) ||
      !ValidOperationId(*operation_id)) {
    return std::nullopt;
  }
  const auto level = ParseDiagnosticLevel(*level_value);
  const auto stage = ParseErrorStage(*stage_value);
  if (!level || !stage) return std::nullopt;
  std::optional<HostErrorCode> code;
  if (const JsonValue* field = value.Find("code")) {
    if (field->string() == nullptr) return std::nullopt;
    code = ParseErrorCode(*field->string());
    if (!code) return std::nullopt;
  }
  return DiagnosticEvent(*unix_ms, *level, *stage, code ? &*code : nullptr,
                         *message, *operation_id, *sequence);
}

std::optional<DiagnosticsSnapshot> ParseDiagnostics(const JsonValue& value,
                                                    std::int64_t limit) {
  if (!OnlyKeys(value,
                {"app_version", "host_version", "core_version",
                 "carrier_policy", "current_carrier", "reconnect_count",
                 "events"}) ||
      value.object()->size() != 7) {
    return std::nullopt;
  }
  const auto* app_version = StringField(value, "app_version");
  const auto* host_version = StringField(value, "host_version");
  const auto* core_version = StringField(value, "core_version");
  const auto* policy = StringField(value, "carrier_policy");
  const auto* carrier_value = StringField(value, "current_carrier");
  const auto* reconnects = IntegerField(value, "reconnect_count");
  const JsonValue* events_value = value.Find("events");
  if (!app_version || !host_version || !core_version || !policy ||
      !carrier_value || !reconnects || !events_value ||
      events_value->array() == nullptr || *policy != "http3-only" ||
      *reconnects < 0 || !SafeText(*app_version, 64) ||
      !SafeText(*host_version, 64) || !SafeText(*core_version, 64) ||
      events_value->array()->size() > static_cast<std::size_t>(limit) ||
      events_value->array()->size() > 256) {
    return std::nullopt;
  }
  const auto carrier = ParseCarrier(*carrier_value);
  if (!carrier) return std::nullopt;
  ::flutter::EncodableList events;
  events.reserve(events_value->array()->size());
  for (const JsonValue& event_value : *events_value->array()) {
    auto event = ParseDiagnosticEvent(event_value);
    if (!event) return std::nullopt;
    events.emplace_back(::flutter::CustomEncodableValue(std::move(*event)));
  }
  return DiagnosticsSnapshot(*app_version, *host_version, *core_version,
                             *policy, *carrier, *reconnects, events);
}

std::string QuotedOrEmpty(const std::string& value) {
  const auto quoted = JsonQuoted(value);
  return quoted.value_or("\"\"");
}

std::string ProfileOperationParams(const std::string& profile_id,
                                   const std::string& operation_id) {
  return "{\"profile_id\":" + QuotedOrEmpty(profile_id) +
         ",\"operation_id\":" + QuotedOrEmpty(operation_id) + "}";
}

}  // namespace

WindowsClientHost::WindowsClientHost(std::unique_ptr<HostService> service)
    : service_(std::move(service)) {}

ServiceResult WindowsClientHost::Call(const std::string& method,
                                      const std::string& params_json) {
  if (!service_) {
    return {std::nullopt,
            ServiceError{"HOST_UNAVAILABLE", "HOST_IPC",
                         "Native host is unavailable.", true, "request"}};
  }
  return service_->Call(method, params_json);
}

void WindowsClientHost::GetCapabilities(
    const HostApiVersion& requested_version,
    std::function<void(ErrorOr<HostCapabilities> reply)> result) {
  if (requested_version.major() < 0 || requested_version.minor() < 0) {
    result(LocalError("UNSUPPORTED_API_VERSION", "HOST_NEGOTIATION",
                      "Host API version is unsupported.", false,
                      "capabilities"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.capabilities",
      "{\"api_major\":" + std::to_string(requested_version.major()) +
          ",\"api_minor\":" + std::to_string(requested_version.minor()) + "}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto capabilities = ParseCapabilities(*response.result);
  if (!capabilities || capabilities->api_version().major() != kHostApiMajor ||
      capabilities->api_version().minor() < kHostApiMinor) {
    result(InvalidResponse());
    return;
  }
  result(std::move(*capabilities));
}

void WindowsClientHost::ListProfiles(
    std::function<void(ErrorOr<::flutter::EncodableList> reply)> result) {
  const ServiceResult response = Call("host.v1.profiles.list", "{}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  if (!OnlyKeys(*response.result, {"profiles"}) ||
      response.result->object()->size() != 1) {
    result(InvalidResponse());
    return;
  }
  const JsonValue* profiles_value = response.result->Find("profiles");
  if (!profiles_value || !profiles_value->array() ||
      profiles_value->array()->size() > 1024) {
    result(InvalidResponse());
    return;
  }
  ::flutter::EncodableList profiles;
  profiles.reserve(profiles_value->array()->size());
  for (const JsonValue& value : *profiles_value->array()) {
    auto profile = ParseProfile(value);
    if (!profile) {
      result(InvalidResponse());
      return;
    }
    profiles.emplace_back(::flutter::CustomEncodableValue(std::move(*profile)));
  }
  result(std::move(profiles));
}

void WindowsClientHost::ImportProfile(
    const ImportProfileRequest& request,
    std::function<void(ErrorOr<ProfileSummary> reply)> result) {
  if (!ValidOnboarding(request.onboarding_value()) ||
      !ValidOperationId(request.operation_id())) {
    result(LocalError("INVALID_PROFILE", "PROFILE_VALIDATION",
                      "Profile import value is invalid.", false,
                      "invalid-operation"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.profiles.import",
      "{\"onboarding_value\":" + QuotedOrEmpty(request.onboarding_value()) +
          ",\"operation_id\":" + QuotedOrEmpty(request.operation_id()) + "}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto profile = ParseProfile(*response.result);
  result(profile ? ErrorOr<ProfileSummary>(std::move(*profile))
                 : ErrorOr<ProfileSummary>(InvalidResponse()));
}

void WindowsClientHost::SelectProfile(
    const SelectProfileRequest& request,
    std::function<void(ErrorOr<ProfileSummary> reply)> result) {
  if (!ValidProfileId(request.profile_id()) ||
      !ValidOperationId(request.operation_id())) {
    result(LocalError("INVALID_PROFILE", "PROFILE_VALIDATION",
                      "Profile selection is invalid.", false,
                      "invalid-operation"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.profiles.select",
      ProfileOperationParams(request.profile_id(), request.operation_id()));
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto profile = ParseProfile(*response.result);
  result(profile ? ErrorOr<ProfileSummary>(std::move(*profile))
                 : ErrorOr<ProfileSummary>(InvalidResponse()));
}

void WindowsClientHost::RemoveProfile(
    const RemoveProfileRequest& request,
    std::function<void(std::optional<FlutterError> reply)> result) {
  if (!ValidProfileId(request.profile_id()) ||
      !ValidOperationId(request.operation_id())) {
    result(LocalError("INVALID_PROFILE", "PROFILE_VALIDATION",
                      "Profile removal is invalid.", false,
                      "invalid-operation"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.profiles.remove",
      "{\"profile_id\":" + QuotedOrEmpty(request.profile_id()) +
          ",\"force\":" + (request.force() ? "true" : "false") +
          ",\"operation_id\":" + QuotedOrEmpty(request.operation_id()) + "}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  const bool* removed = BooleanField(*response.result, "removed");
  if (!OnlyKeys(*response.result, {"removed"}) ||
      response.result->object()->size() != 1 || removed == nullptr ||
      !*removed) {
    result(InvalidResponse());
    return;
  }
  result(std::nullopt);
}

void WindowsClientHost::Connect(
    const ConnectRequest& request,
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  if (!ValidProfileId(request.profile_id()) ||
      !ValidOperationId(request.operation_id())) {
    result(LocalError("INVALID_PROFILE", "PROFILE_VALIDATION",
                      "Connect request is invalid.", false,
                      "invalid-operation"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.tunnel.connect",
      ProfileOperationParams(request.profile_id(), request.operation_id()));
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto status = ParseStatus(*response.result);
  result(status ? ErrorOr<TunnelStatus>(std::move(*status))
                : ErrorOr<TunnelStatus>(InvalidResponse()));
}

void WindowsClientHost::Disconnect(
    const DisconnectRequest& request,
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  if (!ValidOperationId(request.operation_id())) {
    result(LocalError("INTERNAL", "HOST_IPC", "Disconnect request is invalid.",
                      false, "invalid-operation"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.tunnel.disconnect",
      "{\"operation_id\":" + QuotedOrEmpty(request.operation_id()) + "}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto status = ParseStatus(*response.result);
  result(status ? ErrorOr<TunnelStatus>(std::move(*status))
                : ErrorOr<TunnelStatus>(InvalidResponse()));
}

void WindowsClientHost::GetStatus(
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  const ServiceResult response = Call("host.v1.tunnel.status", "{}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto status = ParseStatus(*response.result);
  result(status ? ErrorOr<TunnelStatus>(std::move(*status))
                : ErrorOr<TunnelStatus>(InvalidResponse()));
}

void WindowsClientHost::GetDiagnostics(
    const DiagnosticsRequest& request,
    std::function<void(ErrorOr<DiagnosticsSnapshot> reply)> result) {
  if (request.limit() < 1 || request.limit() > 256) {
    result(LocalError("INTERNAL", "HOST_IPC",
                      "Diagnostics request is invalid.", false,
                      "diagnostics"));
    return;
  }
  const ServiceResult response = Call(
      "host.v1.diagnostics.get",
      "{\"limit\":" + std::to_string(request.limit()) + "}");
  if (!response.ok()) {
    result(ToFlutterError(response.error));
    return;
  }
  auto diagnostics = ParseDiagnostics(*response.result, request.limit());
  result(diagnostics ? ErrorOr<DiagnosticsSnapshot>(std::move(*diagnostics))
                     : ErrorOr<DiagnosticsSnapshot>(InvalidResponse()));
}

}  // namespace neproto_host
