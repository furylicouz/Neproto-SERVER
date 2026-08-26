#include <gtest/gtest.h>

#include <memory>
#include <string>

#include "service_client.h"

namespace neproto_host::test {
namespace {

class FakeTransport final : public ServiceTransport {
 public:
  PipeResponse response{PipeError::kNone, {}};
  std::string request;

  PipeResponse Transact(const std::string& value) override {
    request = value;
    return response;
  }
};

TEST(ServiceClient, BuildsVersionedRequestAndAcceptsMatchingEnvelope) {
  auto transport = std::make_unique<FakeTransport>();
  FakeTransport* fake = transport.get();
  fake->response = {PipeError::kNone,
                    R"({"id":"pigeon-1","ok":true,"result":{"platform":"windows"}})"};
  ServiceClient client(std::move(transport));

  const ServiceResult result =
      client.Call("host.v1.capabilities", R"({"api_major":1,"api_minor":0})");

  ASSERT_TRUE(result.ok());
  EXPECT_EQ(*result.result->Find("platform")->string(), "windows");
  EXPECT_NE(fake->request.find(R"("method":"host.v1.capabilities")"),
            std::string::npos);
}

TEST(ServiceClient, PreservesValidatedStructuredServiceError) {
  auto transport = std::make_unique<FakeTransport>();
  transport->response = {
      PipeError::kNone,
      R"({"id":"pigeon-1","ok":false,"error":"Host API version is unsupported.","host_error":{"code":"UNSUPPORTED_API_VERSION","stage":"HOST_NEGOTIATION","message":"Host API version is unsupported.","retryable":false,"operation_id":"capabilities"}})"};
  ServiceClient client(std::move(transport));

  const ServiceResult result =
      client.Call("host.v1.capabilities", R"({"api_major":2,"api_minor":0})");

  ASSERT_FALSE(result.ok());
  ASSERT_TRUE(result.error.has_value());
  EXPECT_EQ(result.error->code, "UNSUPPORTED_API_VERSION");
  EXPECT_EQ(result.error->stage, "HOST_NEGOTIATION");
}

TEST(ServiceClient, RejectsMismatchedIdUnknownFieldsAndSecretErrors) {
  for (const std::string& response : {
           R"({"id":"other","ok":true,"result":{}})",
           R"({"id":"pigeon-1","ok":true,"result":{},"extra":true})",
           R"({"id":"pigeon-1","ok":false,"error":"np2://secret","host_error":{"code":"INTERNAL","stage":"HOST_IPC","message":"np2://secret","retryable":false,"operation_id":"request"}})"}) {
    auto transport = std::make_unique<FakeTransport>();
    transport->response = {PipeError::kNone, response};
    ServiceClient client(std::move(transport));

    const ServiceResult result = client.Call("host.v1.tunnel.status", "{}");

    ASSERT_FALSE(result.ok());
    ASSERT_TRUE(result.error.has_value());
    EXPECT_EQ(result.error->code, "INTERNAL");
    EXPECT_EQ(result.error->stage, "HOST_IPC");
  }
}

TEST(ServiceClient, MapsUnavailableTransportToStableError) {
  auto transport = std::make_unique<FakeTransport>();
  transport->response = {PipeError::kHostUnavailable, {}};
  ServiceClient client(std::move(transport));

  const ServiceResult result = client.Call("host.v1.tunnel.status", "{}");

  ASSERT_TRUE(result.error.has_value());
  EXPECT_EQ(result.error->code, "HOST_UNAVAILABLE");
  EXPECT_TRUE(result.error->retryable);
}

}  // namespace
}  // namespace neproto_host::test
