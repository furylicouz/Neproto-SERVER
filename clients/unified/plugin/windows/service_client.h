#ifndef FLUTTER_PLUGIN_NEPROTO_SERVICE_CLIENT_H_
#define FLUTTER_PLUGIN_NEPROTO_SERVICE_CLIENT_H_

#include <atomic>
#include <memory>
#include <optional>
#include <string>

#include "json_value.h"
#include "pipe_client.h"

namespace neproto_host {

struct ServiceError {
  std::string code;
  std::string stage;
  std::string message;
  bool retryable;
  std::string operation_id;
};

struct ServiceResult {
  std::optional<JsonValue> result;
  std::optional<ServiceError> error;

  bool ok() const { return result.has_value() && !error.has_value(); }
};

class ServiceTransport {
 public:
  virtual ~ServiceTransport() = default;
  virtual PipeResponse Transact(const std::string& request) = 0;
};

class HostService {
 public:
  virtual ~HostService() = default;
  virtual ServiceResult Call(const std::string& method,
                             const std::string& params_json) = 0;
};

class ServiceClient final : public HostService {
 public:
  explicit ServiceClient(std::unique_ptr<ServiceTransport> transport);

  ServiceClient(const ServiceClient&) = delete;
  ServiceClient& operator=(const ServiceClient&) = delete;

  ServiceResult Call(const std::string& method,
                     const std::string& params_json) override;

 private:
  std::unique_ptr<ServiceTransport> transport_;
  std::atomic<std::uint64_t> sequence_{0};
};

std::unique_ptr<ServiceTransport> CreatePipeServiceTransport();

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_SERVICE_CLIENT_H_
