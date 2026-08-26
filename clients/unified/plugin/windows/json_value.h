#ifndef FLUTTER_PLUGIN_NEPROTO_JSON_VALUE_H_
#define FLUTTER_PLUGIN_NEPROTO_JSON_VALUE_H_

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <variant>
#include <vector>

#include "pipe_client.h"

namespace neproto_host {

class JsonValue {
 public:
  using Array = std::vector<JsonValue>;
  using Object = std::map<std::string, JsonValue, std::less<>>;

  JsonValue();
  explicit JsonValue(bool value);
  explicit JsonValue(std::int64_t value);
  explicit JsonValue(std::string value);
  explicit JsonValue(Array value);
  explicit JsonValue(Object value);

  bool is_null() const;
  const bool* boolean() const;
  const std::int64_t* integer() const;
  const std::string* string() const;
  const Array* array() const;
  const Object* object() const;
  const JsonValue* Find(std::string_view key) const;

 private:
  using Storage =
      std::variant<std::nullptr_t, bool, std::int64_t, std::string, Array,
                   Object>;
  Storage storage_;
};

std::optional<JsonValue> ParseJson(std::string_view input);
std::optional<std::string> JsonQuoted(std::string_view value);

}  // namespace neproto_host

#endif  // FLUTTER_PLUGIN_NEPROTO_JSON_VALUE_H_
