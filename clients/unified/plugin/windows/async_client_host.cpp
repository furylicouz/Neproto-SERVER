#include "async_client_host.h"

#include <condition_variable>
#include <deque>
#include <mutex>
#include <thread>
#include <utility>

namespace neproto_host {
namespace {

constexpr std::size_t kMaxPendingHostTasks = 64;

class SerialExecutor final : public TaskExecutor {
 public:
  SerialExecutor() : worker_([this] { Run(); }) {}

  ~SerialExecutor() override {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      stopping_ = true;
      tasks_.clear();
    }
    condition_.notify_one();
    if (worker_.joinable()) {
      worker_.join();
    }
  }

  bool Post(std::function<void()> task) override {
    if (!task) return false;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      if (stopping_ || tasks_.size() >= kMaxPendingHostTasks) return false;
      tasks_.push_back(std::move(task));
    }
    condition_.notify_one();
    return true;
  }

 private:
  void Run() {
    while (true) {
      std::function<void()> task;
      {
        std::unique_lock<std::mutex> lock(mutex_);
        condition_.wait(lock,
                        [this] { return stopping_ || !tasks_.empty(); });
        if (stopping_) return;
        task = std::move(tasks_.front());
        tasks_.pop_front();
      }
      task();
    }
  }

  std::mutex mutex_;
  std::condition_variable condition_;
  std::deque<std::function<void()>> tasks_;
  bool stopping_ = false;
  std::thread worker_;
};

}  // namespace

AsyncClientHost::AsyncClientHost(std::unique_ptr<ClientHostApi> delegate,
                                 std::unique_ptr<TaskExecutor> executor,
                                 std::shared_ptr<ReplyDispatcher> dispatcher)
    : dispatcher_(std::move(dispatcher)),
      delegate_(std::move(delegate)),
      executor_(std::move(executor)) {}

FlutterError AsyncClientHost::QueueError() {
  ::flutter::EncodableMap details;
  details[::flutter::EncodableValue("code")] =
      ::flutter::EncodableValue("HOST_UNAVAILABLE");
  details[::flutter::EncodableValue("stage")] =
      ::flutter::EncodableValue("HOST_IPC");
  details[::flutter::EncodableValue("retryable")] =
      ::flutter::EncodableValue(true);
  details[::flutter::EncodableValue("operationId")] =
      ::flutter::EncodableValue("queue");
  return FlutterError("HOST_UNAVAILABLE", "Native host queue is unavailable.",
                      ::flutter::EncodableValue(details));
}

void AsyncClientHost::GetCapabilities(
    const HostApiVersion& requested_version,
    std::function<void(ErrorOr<HostCapabilities> reply)> result) {
  const HostApiVersion request = requested_version;
  ClientHostApi* delegate = delegate_.get();
  Schedule<HostCapabilities>(
      [delegate, request](auto callback) {
        delegate->GetCapabilities(request, std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::ListProfiles(
    std::function<void(ErrorOr<::flutter::EncodableList> reply)> result) {
  ClientHostApi* delegate = delegate_.get();
  Schedule<::flutter::EncodableList>(
      [delegate](auto callback) {
        delegate->ListProfiles(std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::ImportProfile(
    const ImportProfileRequest& request,
    std::function<void(ErrorOr<ProfileSummary> reply)> result) {
  const ImportProfileRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  Schedule<ProfileSummary>(
      [delegate, value](auto callback) {
        delegate->ImportProfile(value, std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::SelectProfile(
    const SelectProfileRequest& request,
    std::function<void(ErrorOr<ProfileSummary> reply)> result) {
  const SelectProfileRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  Schedule<ProfileSummary>(
      [delegate, value](auto callback) {
        delegate->SelectProfile(value, std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::RemoveProfile(
    const RemoveProfileRequest& request,
    std::function<void(std::optional<FlutterError> reply)> result) {
  const RemoveProfileRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  auto callback = std::make_shared<
      std::function<void(std::optional<FlutterError>)>>(std::move(result));
  const auto dispatcher = dispatcher_;
  const bool posted = delegate_ && dispatcher_ && executor_ && executor_->Post(
      [delegate, value, callback, dispatcher]() mutable {
        delegate->RemoveProfile(
            value, [callback, dispatcher](std::optional<FlutterError> reply) {
              auto result_value =
                  std::make_shared<std::optional<FlutterError>>(std::move(reply));
              if (dispatcher) {
                dispatcher->Post([callback, result_value]() mutable {
                  (*callback)(std::move(*result_value));
                });
              }
            });
      });
  if (!posted) {
    (*callback)(QueueError());
  }
}

void AsyncClientHost::Connect(
    const ConnectRequest& request,
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  const ConnectRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  Schedule<TunnelStatus>(
      [delegate, value](auto callback) {
        delegate->Connect(value, std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::Disconnect(
    const DisconnectRequest& request,
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  const DisconnectRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  Schedule<TunnelStatus>(
      [delegate, value](auto callback) {
        delegate->Disconnect(value, std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::GetStatus(
    std::function<void(ErrorOr<TunnelStatus> reply)> result) {
  ClientHostApi* delegate = delegate_.get();
  Schedule<TunnelStatus>(
      [delegate](auto callback) {
        delegate->GetStatus(std::move(callback));
      },
      std::move(result));
}

void AsyncClientHost::GetDiagnostics(
    const DiagnosticsRequest& request,
    std::function<void(ErrorOr<DiagnosticsSnapshot> reply)> result) {
  const DiagnosticsRequest value = request;
  ClientHostApi* delegate = delegate_.get();
  Schedule<DiagnosticsSnapshot>(
      [delegate, value](auto callback) {
        delegate->GetDiagnostics(value, std::move(callback));
      },
      std::move(result));
}

std::unique_ptr<TaskExecutor> CreateSerialExecutor() {
  return std::make_unique<SerialExecutor>();
}

}  // namespace neproto_host
