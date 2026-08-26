import 'dart:async';

import 'package:neproto_host/neproto_host.dart';

import 'client_host.dart';

final class FakeClientHost implements ClientHost {
  FakeClientHost({HostCapabilities? capabilities, TunnelStatus? status})
    : capabilities =
          capabilities ??
          HostCapabilities(
            apiVersion: HostApiVersion(major: 1, minor: 0),
            platform: HostPlatform.windows,
            appVersion: '0.1.0-fake',
            hostVersion: '0.1.0-fake',
            coreVersion: '0.1.0-fake',
            supportsHttp3WebTransport: true,
          ),
      status = status ?? _disconnectedStatus();

  final HostCapabilities capabilities;
  TunnelStatus status;
  final List<String> callOrder = <String>[];
  final List<ProfileSummary> profiles = <ProfileSummary>[];
  final StreamController<TunnelStatus> _statusController =
      StreamController<TunnelStatus>.broadcast(sync: true);
  bool _disposed = false;

  @override
  Stream<TunnelStatus> get statusChanges => _statusController.stream;

  void emitStatus(TunnelStatus next) {
    status = next;
    if (!_disposed) {
      _statusController.add(next);
    }
  }

  @override
  Future<HostCapabilities> getCapabilities(
    HostApiVersion requestedVersion,
  ) async {
    callOrder.add('getCapabilities');
    return capabilities;
  }

  @override
  Future<List<ProfileSummary>> listProfiles() async {
    callOrder.add('listProfiles');
    return List<ProfileSummary>.unmodifiable(profiles);
  }

  @override
  Future<ProfileSummary> importProfile(ImportProfileRequest request) {
    throw UnimplementedError('Fake import is added with the profile slice.');
  }

  @override
  Future<ProfileSummary> selectProfile(SelectProfileRequest request) {
    throw UnimplementedError('Fake selection is added with the profile slice.');
  }

  @override
  Future<void> removeProfile(RemoveProfileRequest request) {
    throw UnimplementedError('Fake removal is added with the profile slice.');
  }

  @override
  Future<TunnelStatus> connect(ConnectRequest request) {
    throw UnimplementedError('Fake connection is added with the home slice.');
  }

  @override
  Future<TunnelStatus> disconnect(DisconnectRequest request) {
    throw UnimplementedError(
      'Fake disconnection is added with the home slice.',
    );
  }

  @override
  Future<TunnelStatus> getStatus() async {
    callOrder.add('getStatus');
    return status;
  }

  @override
  Future<DiagnosticsSnapshot> getDiagnostics(DiagnosticsRequest request) async {
    callOrder.add('getDiagnostics');
    return DiagnosticsSnapshot(
      appVersion: capabilities.appVersion,
      hostVersion: capabilities.hostVersion,
      coreVersion: capabilities.coreVersion,
      carrierPolicy: 'http3-only',
      currentCarrier: status.carrier,
      reconnectCount: 0,
      events: <DiagnosticEvent>[],
    );
  }

  @override
  void dispose() {
    if (_disposed) {
      return;
    }
    _disposed = true;
    unawaited(_statusController.close());
  }
}

TunnelStatus _disconnectedStatus() {
  return TunnelStatus(
    state: TunnelState.disconnected,
    carrier: CarrierKind.none,
    connectedAtUnixMs: 0,
    uploadBytesPerSecond: 0,
    downloadBytesPerSecond: 0,
    uploadTotalBytes: 0,
    downloadTotalBytes: 0,
    sequence: 0,
  );
}
