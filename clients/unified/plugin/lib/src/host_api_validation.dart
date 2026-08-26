import 'dart:convert';

abstract final class HostInputValidator {
  static const int maxOperationIdBytes = 64;
  static const int maxOnboardingBytes = 16 * 1024;

  static void validateOperationId(String value) {
    if (value.isEmpty || value.length > maxOperationIdBytes) {
      throw ArgumentError.value(value, 'value', 'invalid operation ID length');
    }
    for (final codeUnit in value.codeUnits) {
      if (codeUnit < 0x21 || codeUnit > 0x7e) {
        throw ArgumentError.value(
          value,
          'value',
          'operation ID must contain printable ASCII without spaces',
        );
      }
    }
  }

  static void validateOnboardingValue(String value) {
    final byteLength = utf8.encode(value).length;
    if (value != value.trim() ||
        byteLength == 0 ||
        byteLength > maxOnboardingBytes ||
        (!value.startsWith('np2://import/v1/') &&
            !value.startsWith('np2://import/v2/'))) {
      throw ArgumentError.value(value, 'value', 'invalid onboarding value');
    }
  }
}
