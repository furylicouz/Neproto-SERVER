import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  group('diagnostic bounds', () {
    test('accepts limits from one through 256', () {
      expect(
        () => HostInputValidator.validateDiagnosticsLimit(1),
        returnsNormally,
      );
      expect(
        () => HostInputValidator.validateDiagnosticsLimit(256),
        returnsNormally,
      );
    });

    test('rejects limits outside the bounded range', () {
      expect(
        () => HostInputValidator.validateDiagnosticsLimit(0),
        throwsArgumentError,
      );
      expect(
        () => HostInputValidator.validateDiagnosticsLimit(257),
        throwsArgumentError,
      );
    });

    test('caps a diagnostic message by UTF-8 byte length', () {
      expect(
        () => HostInputValidator.validateDiagnosticMessage('я' * 256),
        returnsNormally,
      );
      expect(
        () => HostInputValidator.validateDiagnosticMessage('я' * 257),
        throwsArgumentError,
      );
    });

    test('rejects a negative status sequence', () {
      expect(
        () => HostInputValidator.validateStatusSequence(-1),
        throwsArgumentError,
      );
    });
  });

  test(
    'HostError carries stable code and stage instead of a raw exception',
    () {
      final error = HostError(
        code: HostErrorCode.http3Timeout,
        stage: ErrorStage.webTransportConnect,
        message: 'HTTP/3 WebTransport deadline expired.',
        retryable: true,
        operationId: 'connect-01',
      );

      expect(error.code, HostErrorCode.http3Timeout);
      expect(error.stage, ErrorStage.webTransportConnect);
      expect(error.retryable, isTrue);
    },
  );

  test('unknown state and error values never represent success', () {
    expect(TunnelState.unknown, isNot(TunnelState.connected));
    expect(HostErrorCode.unknown, isNot(HostErrorCode.internal));
  });
}
