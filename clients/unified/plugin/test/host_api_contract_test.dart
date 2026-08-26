import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  group('HostInputValidator', () {
    test('accepts a bounded printable operation ID', () {
      expect(
        () => HostInputValidator.validateOperationId('connect-20260826-01'),
        returnsNormally,
      );
    });

    test('rejects empty, whitespace and oversized operation IDs', () {
      for (final value in <String>['', ' leading', 'trailing ', 'a' * 65]) {
        expect(
          () => HostInputValidator.validateOperationId(value),
          throwsArgumentError,
          reason: 'value=$value',
        );
      }
    });

    test('rejects control characters in operation IDs', () {
      expect(
        () => HostInputValidator.validateOperationId('connect\n01'),
        throwsArgumentError,
      );
    });

    test('caps onboarding input by UTF-8 byte length', () {
      expect(
        () => HostInputValidator.validateOnboardingValue('np2://import/v2/x'),
        returnsNormally,
      );
      expect(
        () => HostInputValidator.validateOnboardingValue('я' * 8193),
        throwsArgumentError,
      );
    });
  });

  test('ProfileSummary contains redacted metadata only', () {
    final profile = ProfileSummary(
      id: 'profile-1',
      displayName: 'Primary',
      serverIdentity: 'nepopus.lyntragram.ru',
      host: 'nepopus.lyntragram.ru',
      selected: true,
      hasCredential: true,
      origin: ProfileOrigin.imported,
      catalogManaged: false,
      updatedAtUnixMs: 1787731200000,
    );

    expect(profile.id, 'profile-1');
    expect(profile.hasCredential, isTrue);
    expect(profile.origin, ProfileOrigin.imported);
  });
}
