#ifndef FLUTTER_PLUGIN_NEPROTO_WINDOWS_CLIENT_HOST_H_
#define FLUTTER_PLUGIN_NEPROTO_WINDOWS_CLIENT_HOST_H_

#include <memory>

#include "client_host_api.g.h"
#include "service_client.h"

namespace neproto_host {

class WindowsClientHost final : public ClientHostApi {
 public:
  explicit WindowsClientHost(std::unique_ptr<HostService> service);
  ~WindowsClientHost() override = default;

  WindowsClientHost(const WindowsClientHost&) = delete;
  WindowsClientHost& operator=(const WindowsClientHost&) = delete;

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
  ServiceResult Call(const std::string& method,
                     const std::string& params_json);
  std::unique_ptr<HostService> service_;
};

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_WINDOWS_CLIENT_HOST_H_
