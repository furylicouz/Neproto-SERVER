import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_host/src/host_api_version.dart';

void main() {
  group('HostApiCompatibility', () {
    test('accepts the current major and an equal or newer host minor', () {
      final compatibility = HostApiCompatibility.evaluate(
        requested: const HostApiVersionValue(major: 1, minor: 0),
        provided: const HostApiVersionValue(major: 1, minor: 2),
      );

      expect(compatibility, HostApiCompatibility.compatible);
    });

    test('fails closed when the major version differs', () {
      final compatibility = HostApiCompatibility.evaluate(
        requested: const HostApiVersionValue(major: 1, minor: 0),
        provided: const HostApiVersionValue(major: 2, minor: 0),
      );

      expect(compatibility, HostApiCompatibility.unsupportedMajor);
    });

    test('rejects a host minor older than the requested contract', () {
      final compatibility = HostApiCompatibility.evaluate(
        requested: const HostApiVersionValue(major: 1, minor: 2),
        provided: const HostApiVersionValue(major: 1, minor: 1),
      );

      expect(compatibility, HostApiCompatibility.hostTooOld);
    });

    test('rejects negative version components', () {
      expect(
        () => HostApiCompatibility.evaluate(
          requested: const HostApiVersionValue(major: -1, minor: 0),
          provided: const HostApiVersionValue(major: 1, minor: 0),
        ),
        throwsArgumentError,
      );
    });
  });
}
