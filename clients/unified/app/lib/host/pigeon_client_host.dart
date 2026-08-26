import 'dart:async';

import 'package:neproto_host/neproto_host.dart';

import 'client_host.dart';

final class PigeonClientHost implements ClientHost, ClientHostFlutterApi {
  PigeonClientHost({ClientHostApi? api}) : _api = api ?? ClientHostApi() {
    ClientHostFlutterApi.setUp(this);
  }

  final ClientHostApi _api;
  final StreamController<TunnelStatus> _statusController =
      StreamController<TunnelStatus>.broadcast(sync: true);
  bool _disposed = false;

  @override
  Stream<TunnelStatus> get statusChanges => _statusController.stream;

  @override
  Future<HostCapabilities> getCapabilities(HostApiVersion requestedVersion) =>
      _api.getCapabilities(requestedVersion);

  @override
  Future<List<ProfileSummary>> listProfiles() => _api.listProfiles();

  @override
  Future<ProfileSummary> importProfile(ImportProfileRequest request) =>
      _api.importProfile(request);

  @override
  Future<ProfileSummary> selectProfile(SelectProfileRequest request) =>
      _api.selectProfile(request);

  @override
  Future<void> removeProfile(RemoveProfileRequest request) =>
      _api.removeProfile(request);

  @override
  Future<TunnelStatus> connect(ConnectRequest request) => _api.connect(request);

  @override
  Future<TunnelStatus> disconnect(DisconnectRequest request) =>
      _api.disconnect(request);

  @override
  Future<TunnelStatus> getStatus() => _api.getStatus();

  @override
  Future<DiagnosticsSnapshot> getDiagnostics(DiagnosticsRequest request) =>
      _api.getDiagnostics(request);

  @override
  void statusChanged(TunnelStatus status) {
    if (!_disposed) {
      _statusController.add(status);
    }
  }

  @override
  void dispose() {
    if (_disposed) {
      return;
    }
    _disposed = true;
    ClientHostFlutterApi.setUp(null);
    unawaited(_statusController.close());
  }
}
