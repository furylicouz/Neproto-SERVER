#include <gtest/gtest.h>

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "pipe_client.h"

namespace neproto_host::test {
namespace {

std::vector<std::uint8_t> Frame(const std::string& payload) {
  const auto size = static_cast<std::uint32_t>(payload.size());
  std::vector<std::uint8_t> result{
      static_cast<std::uint8_t>(size), static_cast<std::uint8_t>(size >> 8),
      static_cast<std::uint8_t>(size >> 16),
      static_cast<std::uint8_t>(size >> 24)};
  result.insert(result.end(), payload.begin(), payload.end());
  return result;
}

struct FakeState {
  PipeError connect_error = PipeError::kNone;
  std::vector<std::uint8_t> response;
  std::vector<std::uint8_t> written;
  std::size_t read_offset = 0;
  std::size_t max_chunk = 3;
  int connect_calls = 0;
};

class FakeConnection final : public PipeConnection {
 public:
  explicit FakeConnection(std::shared_ptr<FakeState> state)
      : state_(std::move(state)) {}

  IoResult Read(std::uint8_t* destination, std::size_t length,
                std::chrono::milliseconds) override {
    if (state_->read_offset >= state_->response.size()) {
      return {PipeError::kIoFailure, 0};
    }
    const std::size_t count =
        std::min({length, state_->max_chunk,
                  state_->response.size() - state_->read_offset});
    std::memcpy(destination, state_->response.data() + state_->read_offset,
                count);
    state_->read_offset += count;
    return {PipeError::kNone, count};
  }

  IoResult Write(const std::uint8_t* source, std::size_t length,
                 std::chrono::milliseconds) override {
    const std::size_t count = std::min(length, state_->max_chunk);
    state_->written.insert(state_->written.end(), source, source + count);
    return {PipeError::kNone, count};
  }

 private:
  std::shared_ptr<FakeState> state_;
};

class FakeConnector final : public PipeConnector {
 public:
  explicit FakeConnector(std::shared_ptr<FakeState> state)
      : state_(std::move(state)) {}

  ConnectResult Connect(std::chrono::milliseconds) override {
    state_->connect_calls++;
    if (state_->connect_error != PipeError::kNone) {
      return {state_->connect_error, nullptr};
    }
    return {PipeError::kNone, std::make_unique<FakeConnection>(state_)};
  }

 private:
  std::shared_ptr<FakeState> state_;
};

TEST(PipeClient, FramesPartialReadsAndWritesWithoutService) {
  auto state = std::make_shared<FakeState>();
  state->response = Frame(R"({"id":"1","ok":true})");
  PipeClient client(std::make_unique<FakeConnector>(state));

  const std::string request = R"({"id":"1","method":"status","params":{}})";
  const PipeResponse response = client.Transact(request);

  EXPECT_TRUE(response.ok());
  EXPECT_EQ(response.payload, R"({"id":"1","ok":true})");
  EXPECT_EQ(state->written, Frame(request));
}

TEST(PipeClient, RejectsOversizedRequestBeforeConnecting) {
  auto state = std::make_shared<FakeState>();
  PipeClient client(std::make_unique<FakeConnector>(state));

  const PipeResponse response =
      client.Transact(std::string(kMaxIpcMessageBytes + 1, 'x'));

  EXPECT_EQ(response.error, PipeError::kMessageTooLarge);
  EXPECT_EQ(state->connect_calls, 0);
}

TEST(PipeClient, RejectsOversizedResponseBeforePayloadRead) {
  auto state = std::make_shared<FakeState>();
  const auto size = static_cast<std::uint32_t>(kMaxIpcMessageBytes + 1);
  state->response = {static_cast<std::uint8_t>(size),
                     static_cast<std::uint8_t>(size >> 8),
                     static_cast<std::uint8_t>(size >> 16),
                     static_cast<std::uint8_t>(size >> 24)};
  PipeClient client(std::make_unique<FakeConnector>(state));

  const PipeResponse response = client.Transact("{}");

  EXPECT_EQ(response.error, PipeError::kMessageTooLarge);
  EXPECT_EQ(state->read_offset, 4u);
}

TEST(PipeClient, PreservesStableHostUnavailableError) {
  auto state = std::make_shared<FakeState>();
  state->connect_error = PipeError::kHostUnavailable;
  PipeClient client(std::make_unique<FakeConnector>(state));

  EXPECT_EQ(client.Transact("{}").error, PipeError::kHostUnavailable);
}

}  // namespace
}  // namespace neproto_host::test
