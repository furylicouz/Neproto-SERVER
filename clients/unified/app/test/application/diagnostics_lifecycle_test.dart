import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/application/client_session_controller.dart';
import 'package:neproto_client/host/fake_client_host.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  test(
    'diagnostics refresh accepts bounded redacted native snapshot',
    () async {
      final host = FakeClientHost(diagnostics: diagnosticsSnapshot());
      final controller = ClientSessionController(host);
      addTearDown(controller.dispose);
      await controller.start();

      await controller.refreshDiagnostics();

      expect(controller.state.diagnostics?.carrierPolicy, 'http3-only');
      expect(controller.state.diagnostics?.events, hasLength(1));
      expect(host.diagnosticsRequests.single.limit, 100);
    },
  );

  test('diagnostics containing onboarding data fail closed', () async {
    final unsafe = diagnosticsSnapshot();
    unsafe.events = <DiagnosticEvent>[
      DiagnosticEvent(
        unixMs: 1,
        level: DiagnosticLevel.error,
        stage: ErrorStage.webTransportConnect,
        code: HostErrorCode.internalFailure,
        message: 'np2://import/v2/secret',
        operationId: 'connect-1',
        sequence: 1,
      ),
    ];
    final host = FakeClientHost(diagnostics: unsafe);
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);
    await controller.start();

    await controller.refreshDiagnostics();

    expect(controller.state.diagnostics, isNull);
    expect(controller.state.error?.code, HostErrorCode.internalFailure);
  });

  test('resume refreshes authoritative native status', () async {
    final host = FakeClientHost();
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);
    await controller.start();
    host.status = disconnectedStatus(sequence: 8);

    await controller.refreshFromHost();

    expect(controller.state.status.sequence, 8);
    expect(host.callOrder.where((call) => call == 'getStatus'), hasLength(2));
  });
}

DiagnosticsSnapshot diagnosticsSnapshot() {
  return DiagnosticsSnapshot(
    appVersion: '0.1.0',
    hostVersion: '0.1.0',
    coreVersion: '0.1.0',
    carrierPolicy: 'http3-only',
    currentCarrier: CarrierKind.none,
    reconnectCount: 2,
    events: <DiagnosticEvent>[
      DiagnosticEvent(
        unixMs: 1000,
        level: DiagnosticLevel.error,
        stage: ErrorStage.webTransportConnect,
        code: HostErrorCode.http3Timeout,
        message: 'HTTP/3 WebTransport deadline expired.',
        operationId: 'connect-1',
        sequence: 2,
      ),
    ],
  );
}

TunnelStatus disconnectedStatus({required int sequence}) {
  return TunnelStatus(
    state: TunnelState.disconnected,
    carrier: CarrierKind.none,
    connectedAtUnixMs: 0,
    uploadBytesPerSecond: 0,
    downloadBytesPerSecond: 0,
    uploadTotalBytes: 0,
    downloadTotalBytes: 0,
    sequence: sequence,
  );
}
