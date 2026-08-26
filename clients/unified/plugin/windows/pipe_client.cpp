#include "pipe_client.h"

// This must be included before many other Windows headers.
#define NOMINMAX
#include <windows.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <limits>
#include <utility>

namespace neproto_host {
namespace {

constexpr auto kConnectTimeout = std::chrono::milliseconds(1500);
constexpr auto kRequestTimeout = std::chrono::seconds(12);

class UniqueHandle {
 public:
  explicit UniqueHandle(HANDLE handle = INVALID_HANDLE_VALUE)
      : handle_(handle) {}
  ~UniqueHandle() { Reset(); }

  UniqueHandle(const UniqueHandle&) = delete;
  UniqueHandle& operator=(const UniqueHandle&) = delete;

  UniqueHandle(UniqueHandle&& other) noexcept : handle_(other.Release()) {}
  UniqueHandle& operator=(UniqueHandle&& other) noexcept {
    if (this != &other) {
      Reset(other.Release());
    }
    return *this;
  }

  HANDLE get() const { return handle_; }
  bool valid() const {
    return handle_ != nullptr && handle_ != INVALID_HANDLE_VALUE;
  }

 private:
  HANDLE Release() {
    const HANDLE value = handle_;
    handle_ = INVALID_HANDLE_VALUE;
    return value;
  }

  void Reset(HANDLE next = INVALID_HANDLE_VALUE) {
    if (valid()) {
      CloseHandle(handle_);
    }
    handle_ = next;
  }

  HANDLE handle_;
};

class NamedPipeConnection final : public PipeConnection {
 public:
  explicit NamedPipeConnection(UniqueHandle handle)
      : handle_(std::move(handle)) {}

  IoResult Read(std::uint8_t* destination, std::size_t length,
                std::chrono::milliseconds timeout) override {
    return Transfer(false, destination, length, timeout);
  }

  IoResult Write(const std::uint8_t* source, std::size_t length,
                 std::chrono::milliseconds timeout) override {
    return Transfer(true, const_cast<std::uint8_t*>(source), length, timeout);
  }

 private:
  IoResult Transfer(bool write, std::uint8_t* buffer, std::size_t length,
                    std::chrono::milliseconds timeout) {
    if (!handle_.valid() || buffer == nullptr || length == 0 ||
        length > std::numeric_limits<DWORD>::max()) {
      return {PipeError::kIoFailure, 0};
    }
    UniqueHandle event(CreateEventW(nullptr, TRUE, FALSE, nullptr));
    if (!event.valid()) {
      return {PipeError::kIoFailure, 0};
    }
    OVERLAPPED overlapped{};
    overlapped.hEvent = event.get();
    DWORD transferred = 0;
    const BOOL started = write
                             ? WriteFile(handle_.get(), buffer,
                                         static_cast<DWORD>(length),
                                         &transferred, &overlapped)
                             : ReadFile(handle_.get(), buffer,
                                        static_cast<DWORD>(length),
                                        &transferred, &overlapped);
    if (started) {
      return transferred == 0
                 ? IoResult{PipeError::kIoFailure, 0}
                 : IoResult{PipeError::kNone, transferred};
    }
    if (GetLastError() != ERROR_IO_PENDING) {
      return {PipeError::kIoFailure, 0};
    }
    const auto wait_ms = static_cast<DWORD>(std::clamp<std::int64_t>(
        timeout.count(), 0, std::numeric_limits<DWORD>::max() - 1));
    const DWORD wait_result = WaitForSingleObject(event.get(), wait_ms);
    if (wait_result != WAIT_OBJECT_0) {
      CancelIoEx(handle_.get(), &overlapped);
      WaitForSingleObject(event.get(), INFINITE);
      return {wait_result == WAIT_TIMEOUT ? PipeError::kDeadlineExceeded
                                         : PipeError::kIoFailure,
              0};
    }
    if (!GetOverlappedResult(handle_.get(), &overlapped, &transferred, FALSE) ||
        transferred == 0) {
      return {PipeError::kIoFailure, 0};
    }
    return {PipeError::kNone, transferred};
  }

  UniqueHandle handle_;
};

class NamedPipeConnector final : public PipeConnector {
 public:
  ConnectResult Connect(std::chrono::milliseconds timeout) override {
    const DWORD wait_ms = static_cast<DWORD>(std::clamp<std::int64_t>(
        timeout.count(), 1, std::numeric_limits<DWORD>::max() - 1));
    if (!WaitNamedPipeW(kServicePipePath, wait_ms)) {
      return {GetLastError() == ERROR_SEM_TIMEOUT
                  ? PipeError::kDeadlineExceeded
                  : PipeError::kHostUnavailable,
              nullptr};
    }
    UniqueHandle handle(CreateFileW(
        kServicePipePath, GENERIC_READ | GENERIC_WRITE, 0, nullptr,
        OPEN_EXISTING,
        FILE_FLAG_OVERLAPPED | SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION,
        nullptr));
    if (!handle.valid()) {
      return {PipeError::kHostUnavailable, nullptr};
    }
    DWORD mode = PIPE_READMODE_BYTE;
    if (!SetNamedPipeHandleState(handle.get(), &mode, nullptr, nullptr)) {
      return {PipeError::kIoFailure, nullptr};
    }
    return {PipeError::kNone,
            std::make_unique<NamedPipeConnection>(std::move(handle))};
  }
};

std::chrono::milliseconds Remaining(
    std::chrono::steady_clock::time_point deadline) {
  const auto now = std::chrono::steady_clock::now();
  if (now >= deadline) {
    return std::chrono::milliseconds::zero();
  }
  return std::chrono::duration_cast<std::chrono::milliseconds>(deadline - now);
}

PipeError TransferExact(PipeConnection& connection, bool write,
                        std::uint8_t* buffer, std::size_t length,
                        std::chrono::steady_clock::time_point deadline) {
  std::size_t offset = 0;
  while (offset < length) {
    const auto remaining = Remaining(deadline);
    if (remaining <= std::chrono::milliseconds::zero()) {
      return PipeError::kDeadlineExceeded;
    }
    const IoResult result =
        write ? connection.Write(buffer + offset, length - offset, remaining)
              : connection.Read(buffer + offset, length - offset, remaining);
    if (result.error != PipeError::kNone) {
      return result.error;
    }
    if (result.transferred == 0 || result.transferred > length - offset) {
      return PipeError::kIoFailure;
    }
    offset += result.transferred;
  }
  return PipeError::kNone;
}

std::array<std::uint8_t, 4> FrameHeader(std::size_t length) {
  const auto value = static_cast<std::uint32_t>(length);
  return {static_cast<std::uint8_t>(value),
          static_cast<std::uint8_t>(value >> 8),
          static_cast<std::uint8_t>(value >> 16),
          static_cast<std::uint8_t>(value >> 24)};
}

std::uint32_t DecodeHeader(const std::array<std::uint8_t, 4>& header) {
  return static_cast<std::uint32_t>(header[0]) |
         (static_cast<std::uint32_t>(header[1]) << 8) |
         (static_cast<std::uint32_t>(header[2]) << 16) |
         (static_cast<std::uint32_t>(header[3]) << 24);
}

}  // namespace

PipeClient::PipeClient(std::unique_ptr<PipeConnector> connector)
    : connector_(std::move(connector)) {}

PipeResponse PipeClient::Transact(const std::string& request) {
  if (request.empty() || request.size() > kMaxIpcMessageBytes) {
    return {request.empty() ? PipeError::kMalformedFrame
                            : PipeError::kMessageTooLarge,
            {}};
  }
  if (!connector_) {
    return {PipeError::kHostUnavailable, {}};
  }
  ConnectResult connected = connector_->Connect(kConnectTimeout);
  if (connected.error != PipeError::kNone || !connected.connection) {
    return {connected.error == PipeError::kNone
                ? PipeError::kHostUnavailable
                : connected.error,
            {}};
  }
  const auto deadline = std::chrono::steady_clock::now() + kRequestTimeout;
  auto header = FrameHeader(request.size());
  PipeError error = TransferExact(*connected.connection, true, header.data(),
                                  header.size(), deadline);
  if (error == PipeError::kNone) {
    error = TransferExact(
        *connected.connection, true,
        reinterpret_cast<std::uint8_t*>(const_cast<char*>(request.data())),
        request.size(), deadline);
  }
  if (error != PipeError::kNone) {
    return {error, {}};
  }
  std::array<std::uint8_t, 4> response_header{};
  error = TransferExact(*connected.connection, false, response_header.data(),
                        response_header.size(), deadline);
  if (error != PipeError::kNone) {
    return {error, {}};
  }
  const std::uint32_t response_size = DecodeHeader(response_header);
  if (response_size == 0) {
    return {PipeError::kMalformedFrame, {}};
  }
  if (response_size > kMaxIpcMessageBytes) {
    return {PipeError::kMessageTooLarge, {}};
  }
  std::string response(response_size, '\0');
  error = TransferExact(
      *connected.connection, false,
      reinterpret_cast<std::uint8_t*>(response.data()), response.size(),
      deadline);
  return error == PipeError::kNone ? PipeResponse{PipeError::kNone, response}
                                   : PipeResponse{error, {}};
}

std::unique_ptr<PipeConnector> CreateNamedPipeConnector() {
  return std::make_unique<NamedPipeConnector>();
}

}  // namespace neproto_host
