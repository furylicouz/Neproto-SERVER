#include <gtest/gtest.h>

#include <memory>
#include <string>

#include "windows_client_host.h"

namespace neproto_host::test {
namespace {

class FakeHostService final : public HostService {
 public:
  std::string payload;
  std::string method;
  std::string params;
  int calls = 0;

  ServiceResult Call(const std::string& next_method,
                     const std::string& next_params) override {
    calls++;
    method = next_method;
    params = next_params;
    auto parsed = ParseJson(payload);
    if (!parsed) {
      return {std::nullopt,
              ServiceError{"INTERNAL", "HOST_IPC", "Invalid fixture.",
                           false, "fixture"}};
    }
    return {std::move(*parsed), std::nullopt};
  }
};

TEST(WindowsClientHost, MapsCapabilitiesThroughGeneratedContract) {
  auto service = std::make_unique<FakeHostService>();
  FakeHostService* fake = service.get();
  fake->payload = R"({"api_version":{"major":1,"minor":0},"platform":"windows","app_version":"0.1.0","host_version":"0.1.0","core_version":"0.1.0","supports_http3_web_transport":true})";
  WindowsClientHost host(std::move(service));

  host.GetCapabilities(
      HostApiVersion(1, 0), [&](ErrorOr<HostCapabilities> reply) {
        ASSERT_FALSE(reply.has_error());
        EXPECT_EQ(reply.value().platform(), HostPlatform::kWindows);
        EXPECT_TRUE(reply.value().supports_http3_web_transport());
      });

  EXPECT_EQ(fake->method, "host.v1.capabilities");
  EXPECT_NE(fake->params.find(R"("api_major":1)"), std::string::npos);
}

TEST(WindowsClientHost, RejectsNonHttp3ConnectedStatus) {
  auto service = std::make_unique<FakeHostService>();
  service->payload = R"({"state":"connected","profile_id":"primary","carrier":"unknown","connected_at_unix_ms":1,"upload_bytes_per_second":0,"download_bytes_per_second":0,"upload_total_bytes":0,"download_total_bytes":0,"sequence":2})";
  WindowsClientHost host(std::move(service));

  host.GetStatus([](ErrorOr<TunnelStatus> reply) {
    ASSERT_TRUE(reply.has_error());
    EXPECT_EQ(reply.error().code(), "INTERNAL");
  });
}

TEST(WindowsClientHost, InvalidOnboardingFailsBeforeServiceCall) {
  auto service = std::make_unique<FakeHostService>();
  FakeHostService* fake = service.get();
  WindowsClientHost host(std::move(service));

  host.ImportProfile(
      ImportProfileRequest("https://not-onboarding", "import-1"),
      [](ErrorOr<ProfileSummary> reply) {
        ASSERT_TRUE(reply.has_error());
        EXPECT_EQ(reply.error().code(), "INVALID_PROFILE");
      });

  EXPECT_EQ(fake->calls, 0);
}

TEST(WindowsClientHost, DiagnosticsRejectOnboardingMaterial) {
  auto service = std::make_unique<FakeHostService>();
  service->payload = R"({"app_version":"0.1.0","host_version":"0.1.0","core_version":"0.1.0","carrier_policy":"http3-only","current_carrier":"none","reconnect_count":0,"events":[{"unix_ms":1,"level":"error","stage":"HOST_IPC","message":"np2://import/v2/secret","operation_id":"diagnostic-1","sequence":1}]})";
  WindowsClientHost host(std::move(service));

  host.GetDiagnostics(DiagnosticsRequest(100),
                      [](ErrorOr<DiagnosticsSnapshot> reply) {
                        ASSERT_TRUE(reply.has_error());
                        EXPECT_EQ(reply.error().code(), "INTERNAL");
                      });
}

}  // namespace
}  // namespace neproto_host::test
