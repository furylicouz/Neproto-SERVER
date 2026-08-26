#include <gtest/gtest.h>

#include <string>

#include "json_value.h"

namespace neproto_host::test {

TEST(JsonValue, ParsesStrictHostEnvelope) {
  const auto value = ParseJson(
      R"({"id":"status-1","ok":true,"result":{"sequence":7,"carrier":"http3_webtransport"}})");

  ASSERT_TRUE(value.has_value());
  ASSERT_NE(value->object(), nullptr);
  ASSERT_NE(value->Find("result"), nullptr);
  const JsonValue* sequence = value->Find("result")->Find("sequence");
  ASSERT_NE(sequence, nullptr);
  ASSERT_NE(sequence->integer(), nullptr);
  EXPECT_EQ(*sequence->integer(), 7);
}

TEST(JsonValue, RejectsDuplicateTrailingInvalidAndDeepInput) {
  EXPECT_FALSE(ParseJson(R"({"id":"1","id":"2"})").has_value());
  EXPECT_FALSE(ParseJson(R"({"id":"1"} {})").has_value());
  EXPECT_FALSE(ParseJson(std::string("{\"value\":\"") +
                         static_cast<char>(0xff) + "\"}")
                   .has_value());
  EXPECT_FALSE(ParseJson(std::string(40, '[') + "0" + std::string(40, ']'))
                   .has_value());
  EXPECT_FALSE(ParseJson(R"({"number":1.5})").has_value());
}

TEST(JsonValue, DecodesUnicodeAndQuotesSecretsSafely) {
  const auto value = ParseJson(R"({"name":"\u041d\u0435\u041f\u0440\u043e\u0442\u043e"})");
  ASSERT_TRUE(value.has_value());
  ASSERT_NE(value->Find("name"), nullptr);
  EXPECT_EQ(*value->Find("name")->string(), "НеПрото");

  const auto quoted = JsonQuoted("np2://value/\"line\n");
  ASSERT_TRUE(quoted.has_value());
  EXPECT_EQ(*quoted, R"("np2://value/\"line\n")");
}

TEST(JsonValue, RejectsOversizedDocumentBeforeParsing) {
  EXPECT_FALSE(ParseJson(std::string(kMaxIpcMessageBytes + 1, ' ')).has_value());
}

}  // namespace neproto_host::test
