#ifndef FLUTTER_PLUGIN_NEPROTO_PIPE_CLIENT_H_
#define FLUTTER_PLUGIN_NEPROTO_PIPE_CLIENT_H_

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace neproto_host {

inline constexpr std::size_t kMaxIpcMessageBytes = 256 * 1024;
inline constexpr wchar_t kServicePipePath[] =
    LR"(\\.\pipe\NeProto.Service.v1)";

enum class PipeError {
  kNone,
  kHostUnavailable,
  kDeadlineExceeded,
  kMalformedFrame,
  kMessageTooLarge,
  kIoFailure,
};

struct IoResult {
  PipeError error;
  std::size_t transferred;
};

class PipeConnection {
 public:
  virtual ~PipeConnection() = default;
  virtual IoResult Read(std::uint8_t* destination, std::size_t length,
                        std::chrono::milliseconds timeout) = 0;
  virtual IoResult Write(const std::uint8_t* source, std::size_t length,
                         std::chrono::milliseconds timeout) = 0;
};

struct ConnectResult {
  PipeError error;
  std::unique_ptr<PipeConnection> connection;
};

class PipeConnector {
 public:
  virtual ~PipeConnector() = default;
  virtual ConnectResult Connect(std::chrono::milliseconds timeout) = 0;
};

struct PipeResponse {
  PipeError error;
  std::string payload;

  bool ok() const { return error == PipeError::kNone; }
};

class PipeClient {
 public:
  explicit PipeClient(std::unique_ptr<PipeConnector> connector);

  PipeClient(const PipeClient&) = delete;
  PipeClient& operator=(const PipeClient&) = delete;

  PipeResponse Transact(const std::string& request);

 private:
  std::unique_ptr<PipeConnector> connector_;
};

std::unique_ptr<PipeConnector> CreateNamedPipeConnector();

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_PIPE_CLIENT_H_
