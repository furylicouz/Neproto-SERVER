import 'package:flutter_test/flutter_test.dart';
import 'package:neproto_client/application/client_session_controller.dart';
import 'package:neproto_client/host/fake_client_host.dart';
import 'package:neproto_host/neproto_host.dart';

void main() {
  test(
    'connect and disconnect commands are guarded and carry operation IDs',
    () async {
      final host = FakeClientHost(
        status: status(TunnelState.disconnected, sequence: 1),
      );
      final controller = ClientSessionController(host);
      addTearDown(controller.dispose);
      await controller.start();

      await controller.connect('profile-1');
      expect(controller.state.status.state, TunnelState.connecting);
      expect(host.connectRequests, hasLength(1));
      expect(host.connectRequests.single.profileId, 'profile-1');
      expect(host.connectRequests.single.operationId, startsWith('connect-'));

      await controller.connect('profile-1');
      expect(host.connectRequests, hasLength(1));

      host.emitStatus(status(TunnelState.connected, sequence: 3));
      await controller.disconnect();
      expect(controller.state.status.state, TunnelState.disconnecting);
      expect(host.disconnectRequests, hasLength(1));
      expect(
        host.disconnectRequests.single.operationId,
        startsWith('disconnect-'),
      );
    },
  );
}

TunnelStatus status(TunnelState state, {required int sequence}) {
  return TunnelStatus(
    state: state,
    profileId: 'profile-1',
    carrier: state == TunnelState.connected
        ? CarrierKind.http3WebTransport
        : CarrierKind.none,
    connectedAtUnixMs: 0,
    uploadBytesPerSecond: 0,
    downloadBytesPerSecond: 0,
    uploadTotalBytes: 0,
    downloadTotalBytes: 0,
    sequence: sequence,
  );
}
