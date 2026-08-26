#include <gtest/gtest.h>

#include <functional>
#include <memory>
#include <string>
#include <utility>

#include "async_client_host.h"
#include "windows_client_host.h"

namespace neproto_host::test {
namespace {

class StatusService final : public HostService {
 public:
  ServiceResult Call(const std::string&, const std::string&) override {
    return {ParseJson(R"({"state":"disconnected","carrier":"none","connected_at_unix_ms":0,"upload_bytes_per_second":0,"download_bytes_per_second":0,"upload_total_bytes":0,"download_total_bytes":0,"sequence":4})"),
            std::nullopt};
  }
};

class ManualExecutor final : public TaskExecutor {
 public:
  bool Post(std::function<void()> value) override {
    task = std::move(value);
    return true;
  }
  std::function<void()> task;
};

class ManualDispatcher final : public ReplyDispatcher {
 public:
  bool Post(std::function<void()> value) override {
    task = std::move(value);
    return true;
  }
  std::function<void()> task;
};

TEST(AsyncClientHost, DefersServiceWorkAndMarshalsReply) {
  auto executor = std::make_unique<ManualExecutor>();
  ManualExecutor* executor_pointer = executor.get();
  auto dispatcher = std::make_shared<ManualDispatcher>();
  AsyncClientHost host(
      std::make_unique<WindowsClientHost>(std::make_unique<StatusService>()),
      std::move(executor), dispatcher);
  bool replied = false;

  host.GetStatus([&](ErrorOr<TunnelStatus> reply) {
    ASSERT_FALSE(reply.has_error());
    EXPECT_EQ(reply.value().sequence(), 4);
    replied = true;
  });

  EXPECT_FALSE(replied);
  ASSERT_TRUE(executor_pointer->task);
  executor_pointer->task();
  EXPECT_FALSE(replied);
  ASSERT_TRUE(dispatcher->task);
  dispatcher->task();
  EXPECT_TRUE(replied);
}

}  // namespace
}  // namespace neproto_host::test
