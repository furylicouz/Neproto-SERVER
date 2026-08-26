#ifndef FLUTTER_PLUGIN_NEPROTO_ASYNC_CLIENT_HOST_H_
#define FLUTTER_PLUGIN_NEPROTO_ASYNC_CLIENT_HOST_H_

#include <functional>
#include <memory>
#include <optional>

#include "client_host_api.g.h"

namespace neproto_host {

class TaskExecutor {
 public:
  virtual ~TaskExecutor() = default;
  virtual bool Post(std::function<void()> task) = 0;
};

class ReplyDispatcher {
 public:
  virtual ~ReplyDispatcher() = default;
  virtual bool Post(std::function<void()> task) = 0;
};

class AsyncClientHost final : public ClientHostApi {
 public:
  AsyncClientHost(std::unique_ptr<ClientHostApi> delegate,
                  std::unique_ptr<TaskExecutor> executor,
                  std::shared_ptr<ReplyDispatcher> dispatcher);
  ~AsyncClientHost() override = default;

  AsyncClientHost(const AsyncClientHost&) = delete;
  AsyncClientHost& operator=(const AsyncClientHost&) = delete;

  void GetCapabilities(
      const HostApiVersion& requested_version,
      std::function<void(ErrorOr<HostCapabilities> reply)> result) override;
  void ListProfiles(
      std::function<void(ErrorOr<::flutter::EncodableList> reply)> result)
      override;
  void ImportProfile(
      const ImportProfileRequest& request,
      std::function<void(ErrorOr<ProfileSummary> reply)> result) override;
  void SelectProfile(
      const SelectProfileRequest& request,
      std::function<void(ErrorOr<ProfileSummary> reply)> result) override;
  void RemoveProfile(
      const RemoveProfileRequest& request,
      std::function<void(std::optional<FlutterError> reply)> result) override;
  void Connect(const ConnectRequest& request,
               std::function<void(ErrorOr<TunnelStatus> reply)> result)
      override;
  void Disconnect(
      const DisconnectRequest& request,
      std::function<void(ErrorOr<TunnelStatus> reply)> result) override;
  void GetStatus(
      std::function<void(ErrorOr<TunnelStatus> reply)> result) override;
  void GetDiagnostics(
      const DiagnosticsRequest& request,
      std::function<void(ErrorOr<DiagnosticsSnapshot> reply)> result) override;

 private:
  template <typename T>
  void Schedule(
      std::function<void(std::function<void(ErrorOr<T>)>)> operation,
      std::function<void(ErrorOr<T>)> result) {
    auto callback =
        std::make_shared<std::function<void(ErrorOr<T>)>>(std::move(result));
    const auto dispatcher = dispatcher_;
    const bool posted = delegate_ && dispatcher_ && executor_ && executor_->Post(
        [operation = std::move(operation), callback, dispatcher]() mutable {
          operation([callback, dispatcher](ErrorOr<T> reply) mutable {
            auto value = std::make_shared<ErrorOr<T>>(std::move(reply));
            if (dispatcher) {
              dispatcher->Post([callback, value]() mutable {
                (*callback)(std::move(*value));
              });
            }
          });
        });
    if (!posted) {
      (*callback)(QueueError());
    }
  }

  static FlutterError QueueError();

  std::shared_ptr<ReplyDispatcher> dispatcher_;
  std::unique_ptr<ClientHostApi> delegate_;
  std::unique_ptr<TaskExecutor> executor_;
};

std::unique_ptr<TaskExecutor> CreateSerialExecutor();

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_ASYNC_CLIENT_HOST_H_
