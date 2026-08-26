#include "json_value.h"

#include <charconv>
#include <cstddef>
#include <cstdint>
#include <utility>

namespace neproto_host {
namespace {

constexpr std::size_t kMaxJsonDepth = 32;
constexpr std::size_t kMaxJsonNodes = 4096;

bool IsValidUtf8(std::string_view input) {
  for (std::size_t index = 0; index < input.size();) {
    const auto lead = static_cast<unsigned char>(input[index]);
    std::size_t continuation = 0;
    std::uint32_t code_point = 0;
    if (lead <= 0x7f) {
      index++;
      continue;
    }
    if (lead >= 0xc2 && lead <= 0xdf) {
      continuation = 1;
      code_point = lead & 0x1f;
    } else if (lead >= 0xe0 && lead <= 0xef) {
      continuation = 2;
      code_point = lead & 0x0f;
    } else if (lead >= 0xf0 && lead <= 0xf4) {
      continuation = 3;
      code_point = lead & 0x07;
    } else {
      return false;
    }
    if (index + continuation >= input.size()) {
      return false;
    }
    for (std::size_t offset = 1; offset <= continuation; offset++) {
      const auto byte = static_cast<unsigned char>(input[index + offset]);
      if ((byte & 0xc0) != 0x80) {
        return false;
      }
      code_point = (code_point << 6) | (byte & 0x3f);
    }
    if ((continuation == 2 && code_point < 0x800) ||
        (continuation == 3 && code_point < 0x10000) ||
        code_point > 0x10ffff ||
        (code_point >= 0xd800 && code_point <= 0xdfff)) {
      return false;
    }
    index += continuation + 1;
  }
  return true;
}

void AppendUtf8(std::uint32_t code_point, std::string* output) {
  if (code_point <= 0x7f) {
    output->push_back(static_cast<char>(code_point));
  } else if (code_point <= 0x7ff) {
    output->push_back(static_cast<char>(0xc0 | (code_point >> 6)));
    output->push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
  } else if (code_point <= 0xffff) {
    output->push_back(static_cast<char>(0xe0 | (code_point >> 12)));
    output->push_back(static_cast<char>(0x80 | ((code_point >> 6) & 0x3f)));
    output->push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
  } else {
    output->push_back(static_cast<char>(0xf0 | (code_point >> 18)));
    output->push_back(static_cast<char>(0x80 | ((code_point >> 12) & 0x3f)));
    output->push_back(static_cast<char>(0x80 | ((code_point >> 6) & 0x3f)));
    output->push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
  }
}

int HexValue(char value) {
  if (value >= '0' && value <= '9') {
    return value - '0';
  }
  if (value >= 'a' && value <= 'f') {
    return 10 + value - 'a';
  }
  if (value >= 'A' && value <= 'F') {
    return 10 + value - 'A';
  }
  return -1;
}

class Parser {
 public:
  explicit Parser(std::string_view input) : input_(input) {}

  std::optional<JsonValue> Parse() {
    SkipWhitespace();
    auto value = ParseValue(0);
    SkipWhitespace();
    if (!value || position_ != input_.size()) {
      return std::nullopt;
    }
    return value;
  }

 private:
  std::optional<JsonValue> ParseValue(std::size_t depth) {
    if (depth > kMaxJsonDepth || ++nodes_ > kMaxJsonNodes ||
        position_ >= input_.size()) {
      return std::nullopt;
    }
    switch (input_[position_]) {
      case 'n':
        return ConsumeLiteral("null") ? std::optional<JsonValue>(JsonValue())
                                      : std::nullopt;
      case 't':
        return ConsumeLiteral("true")
                   ? std::optional<JsonValue>(JsonValue(true))
                   : std::nullopt;
      case 'f':
        return ConsumeLiteral("false")
                   ? std::optional<JsonValue>(JsonValue(false))
                   : std::nullopt;
      case '"': {
        auto value = ParseString();
        return value ? std::optional<JsonValue>(JsonValue(std::move(*value)))
                     : std::nullopt;
      }
      case '[':
        return ParseArray(depth + 1);
      case '{':
        return ParseObject(depth + 1);
      default:
        return ParseInteger();
    }
  }

  std::optional<JsonValue> ParseArray(std::size_t depth) {
    position_++;
    SkipWhitespace();
    JsonValue::Array values;
    if (Consume(']')) {
      return JsonValue(std::move(values));
    }
    while (true) {
      SkipWhitespace();
      auto value = ParseValue(depth);
      if (!value) {
        return std::nullopt;
      }
      values.push_back(std::move(*value));
      SkipWhitespace();
      if (Consume(']')) {
        return JsonValue(std::move(values));
      }
      if (!Consume(',')) {
        return std::nullopt;
      }
    }
  }

  std::optional<JsonValue> ParseObject(std::size_t depth) {
    position_++;
    SkipWhitespace();
    JsonValue::Object values;
    if (Consume('}')) {
      return JsonValue(std::move(values));
    }
    while (true) {
      SkipWhitespace();
      auto key = ParseString();
      SkipWhitespace();
      if (!key || !Consume(':')) {
        return std::nullopt;
      }
      SkipWhitespace();
      auto value = ParseValue(depth);
      if (!value || !values.emplace(std::move(*key), std::move(*value)).second) {
        return std::nullopt;
      }
      SkipWhitespace();
      if (Consume('}')) {
        return JsonValue(std::move(values));
      }
      if (!Consume(',')) {
        return std::nullopt;
      }
    }
  }

  std::optional<JsonValue> ParseInteger() {
    const std::size_t start = position_;
    if (position_ < input_.size() && input_[position_] == '-') {
      position_++;
    }
    if (position_ >= input_.size()) {
      return std::nullopt;
    }
    if (input_[position_] == '0') {
      position_++;
      if (position_ < input_.size() && input_[position_] >= '0' &&
          input_[position_] <= '9') {
        return std::nullopt;
      }
    } else {
      const std::size_t first_digit = position_;
      while (position_ < input_.size() && input_[position_] >= '0' &&
             input_[position_] <= '9') {
        position_++;
      }
      if (position_ == first_digit) {
        return std::nullopt;
      }
    }
    std::int64_t value = 0;
    const auto conversion = std::from_chars(input_.data() + start,
                                            input_.data() + position_, value);
    if (conversion.ec != std::errc{} ||
        conversion.ptr != input_.data() + position_) {
      return std::nullopt;
    }
    return JsonValue(value);
  }

  std::optional<std::string> ParseString() {
    if (!Consume('"')) {
      return std::nullopt;
    }
    std::string value;
    while (position_ < input_.size()) {
      const unsigned char character =
          static_cast<unsigned char>(input_[position_++]);
      if (character == '"') {
        return value;
      }
      if (character < 0x20) {
        return std::nullopt;
      }
      if (character != '\\') {
        value.push_back(static_cast<char>(character));
        continue;
      }
      if (position_ >= input_.size()) {
        return std::nullopt;
      }
      const char escape = input_[position_++];
      switch (escape) {
        case '"':
        case '\\':
        case '/':
          value.push_back(escape);
          break;
        case 'b':
          value.push_back('\b');
          break;
        case 'f':
          value.push_back('\f');
          break;
        case 'n':
          value.push_back('\n');
          break;
        case 'r':
          value.push_back('\r');
          break;
        case 't':
          value.push_back('\t');
          break;
        case 'u': {
          auto code_point = ParseHexQuad();
          if (!code_point) {
            return std::nullopt;
          }
          if (*code_point >= 0xd800 && *code_point <= 0xdbff) {
            if (position_ + 2 > input_.size() || input_[position_] != '\\' ||
                input_[position_ + 1] != 'u') {
              return std::nullopt;
            }
            position_ += 2;
            auto low = ParseHexQuad();
            if (!low || *low < 0xdc00 || *low > 0xdfff) {
              return std::nullopt;
            }
            *code_point = 0x10000 + ((*code_point - 0xd800) << 10) +
                          (*low - 0xdc00);
          } else if (*code_point >= 0xdc00 && *code_point <= 0xdfff) {
            return std::nullopt;
          }
          AppendUtf8(*code_point, &value);
          break;
        }
        default:
          return std::nullopt;
      }
    }
    return std::nullopt;
  }

  std::optional<std::uint32_t> ParseHexQuad() {
    if (position_ + 4 > input_.size()) {
      return std::nullopt;
    }
    std::uint32_t value = 0;
    for (int index = 0; index < 4; index++) {
      const int digit = HexValue(input_[position_++]);
      if (digit < 0) {
        return std::nullopt;
      }
      value = (value << 4) | static_cast<std::uint32_t>(digit);
    }
    return value;
  }

  bool ConsumeLiteral(std::string_view literal) {
    if (input_.substr(position_, literal.size()) != literal) {
      return false;
    }
    position_ += literal.size();
    return true;
  }

  bool Consume(char expected) {
    if (position_ >= input_.size() || input_[position_] != expected) {
      return false;
    }
    position_++;
    return true;
  }

  void SkipWhitespace() {
    while (position_ < input_.size() &&
           (input_[position_] == ' ' || input_[position_] == '\n' ||
            input_[position_] == '\r' || input_[position_] == '\t')) {
      position_++;
    }
  }

  std::string_view input_;
  std::size_t position_ = 0;
  std::size_t nodes_ = 0;
};

}  // namespace

JsonValue::JsonValue() : storage_(nullptr) {}
JsonValue::JsonValue(bool value) : storage_(value) {}
JsonValue::JsonValue(std::int64_t value) : storage_(value) {}
JsonValue::JsonValue(std::string value) : storage_(std::move(value)) {}
JsonValue::JsonValue(Array value) : storage_(std::move(value)) {}
JsonValue::JsonValue(Object value) : storage_(std::move(value)) {}

bool JsonValue::is_null() const {
  return std::holds_alternative<std::nullptr_t>(storage_);
}
const bool* JsonValue::boolean() const { return std::get_if<bool>(&storage_); }
const std::int64_t* JsonValue::integer() const {
  return std::get_if<std::int64_t>(&storage_);
}
const std::string* JsonValue::string() const {
  return std::get_if<std::string>(&storage_);
}
const JsonValue::Array* JsonValue::array() const {
  return std::get_if<Array>(&storage_);
}
const JsonValue::Object* JsonValue::object() const {
  return std::get_if<Object>(&storage_);
}
const JsonValue* JsonValue::Find(std::string_view key) const {
  const Object* values = object();
  if (values == nullptr) {
    return nullptr;
  }
  const auto iterator = values->find(key);
  return iterator == values->end() ? nullptr : &iterator->second;
}

std::optional<JsonValue> ParseJson(std::string_view input) {
  if (input.empty() || input.size() > kMaxIpcMessageBytes ||
      !IsValidUtf8(input)) {
    return std::nullopt;
  }
  return Parser(input).Parse();
}

std::optional<std::string> JsonQuoted(std::string_view value) {
  if (!IsValidUtf8(value)) {
    return std::nullopt;
  }
  std::string result;
  result.reserve(value.size() + 2);
  result.push_back('"');
  constexpr char kHex[] = "0123456789abcdef";
  for (const unsigned char character : value) {
    switch (character) {
      case '"':
        result += "\\\"";
        break;
      case '\\':
        result += "\\\\";
        break;
      case '\b':
        result += "\\b";
        break;
      case '\f':
        result += "\\f";
        break;
      case '\n':
        result += "\\n";
        break;
      case '\r':
        result += "\\r";
        break;
      case '\t':
        result += "\\t";
        break;
      default:
        if (character < 0x20) {
          result += "\\u00";
          result.push_back(kHex[character >> 4]);
          result.push_back(kHex[character & 0x0f]);
        } else {
          result.push_back(static_cast<char>(character));
        }
    }
  }
  result.push_back('"');
  return result;
}

}  // namespace neproto_host
