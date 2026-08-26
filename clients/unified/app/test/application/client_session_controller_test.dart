import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/application/client_session_controller.dart';
import 'package:neproto_client/host/fake_client_host.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  test(
    'startup negotiates host API before refreshing authoritative status',
    () async {
      final host = FakeClientHost(
        capabilities: HostCapabilities(
          apiVersion: HostApiVersion(major: 1, minor: 0),
          platform: HostPlatform.windows,
          appVersion: '0.1.0',
          hostVersion: '0.1.0',
          coreVersion: '0.1.0',
          supportsHttp3WebTransport: true,
        ),
        status: disconnectedStatus(sequence: 7),
      );
      final controller = ClientSessionController(host);
      addTearDown(controller.dispose);

      await controller.start();

      expect(host.callOrder, <String>[
        'getCapabilities',
        'getStatus',
        'listProfiles',
      ]);
      expect(controller.state.ready, isTrue);
      expect(controller.state.capabilities?.platform, HostPlatform.windows);
      expect(controller.state.status.sequence, 7);
      expect(controller.state.error, isNull);
    },
  );

  test('unsupported host major fails closed before status refresh', () async {
    final host = FakeClientHost(
      capabilities: HostCapabilities(
        apiVersion: HostApiVersion(major: 2, minor: 0),
        platform: HostPlatform.ios,
        appVersion: '0.1.0',
        hostVersion: '2.0.0',
        coreVersion: '0.1.0',
        supportsHttp3WebTransport: true,
      ),
      status: disconnectedStatus(sequence: 1),
    );
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);

    await controller.start();

    expect(host.callOrder, <String>['getCapabilities']);
    expect(controller.state.ready, isFalse);
    expect(controller.state.error?.code, HostErrorCode.unsupportedApiVersion);
    expect(controller.state.error?.stage, ErrorStage.hostNegotiation);
  });

  test('startup subscribes to monotonic native status callbacks', () async {
    final host = FakeClientHost(status: disconnectedStatus(sequence: 2));
    final controller = ClientSessionController(host);
    addTearDown(controller.dispose);
    await controller.start();

    host.emitStatus(
      TunnelStatus(
        state: TunnelState.connecting,
        profileId: 'profile-1',
        carrier: CarrierKind.none,
        connectedAtUnixMs: 0,
        uploadBytesPerSecond: 0,
        downloadBytesPerSecond: 0,
        uploadTotalBytes: 0,
        downloadTotalBytes: 0,
        sequence: 4,
      ),
    );
    host.emitStatus(disconnectedStatus(sequence: 3));
    await Future<void>.delayed(Duration.zero);

    expect(controller.state.status.state, TunnelState.connecting);
    expect(controller.state.status.sequence, 4);
  });
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
