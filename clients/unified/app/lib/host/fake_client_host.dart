import 'dart:async';

import 'package:neproto_host/neproto_host.dart';

import 'client_host.dart';

final class FakeClientHost implements ClientHost {
  FakeClientHost({
    HostCapabilities? capabilities,
    TunnelStatus? status,
    List<ProfileSummary>? profiles,
    this.onImport,
  }) : capabilities =
           capabilities ??
           HostCapabilities(
             apiVersion: HostApiVersion(major: 1, minor: 0),
             platform: HostPlatform.windows,
             appVersion: '0.1.0-fake',
             hostVersion: '0.1.0-fake',
             coreVersion: '0.1.0-fake',
             supportsHttp3WebTransport: true,
           ),
       status = status ?? _disconnectedStatus(),
       profiles = List<ProfileSummary>.of(profiles ?? <ProfileSummary>[]);

  final HostCapabilities capabilities;
  final void Function(ImportProfileRequest request)? onImport;
  TunnelStatus status;
  final List<String> callOrder = <String>[];
  final List<ProfileSummary> profiles;
  final List<ConnectRequest> connectRequests = <ConnectRequest>[];
  final List<DisconnectRequest> disconnectRequests = <DisconnectRequest>[];
  final List<SelectProfileRequest> selectRequests = <SelectProfileRequest>[];
  final List<RemoveProfileRequest> removeRequests = <RemoveProfileRequest>[];
  int importCalls = 0;
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
  Future<ProfileSummary> importProfile(ImportProfileRequest request) async {
    callOrder.add('importProfile');
    importCalls++;
    onImport?.call(request);
    for (var index = 0; index < profiles.length; index++) {
      profiles[index] = _copyProfile(profiles[index], selected: false);
    }
    final imported = ProfileSummary(
      id: 'imported-$importCalls',
      displayName: 'Imported profile $importCalls',
      serverIdentity: 'vpn.example.test',
      host: 'vpn.example.test',
      selected: true,
      hasCredential: true,
      origin: ProfileOrigin.imported,
      catalogManaged: false,
      updatedAtUnixMs: importCalls,
    );
    profiles.add(imported);
    return imported;
  }

  @override
  Future<ProfileSummary> selectProfile(SelectProfileRequest request) async {
    callOrder.add('selectProfile');
    selectRequests.add(request);
    final selectedIndex = profiles.indexWhere(
      (item) => item.id == request.profileId,
    );
    if (selectedIndex < 0) {
      throw StateError('profile not found');
    }
    for (var index = 0; index < profiles.length; index++) {
      profiles[index] = _copyProfile(
        profiles[index],
        selected: index == selectedIndex,
      );
    }
    return profiles[selectedIndex];
  }

  @override
  Future<void> removeProfile(RemoveProfileRequest request) async {
    callOrder.add('removeProfile');
    removeRequests.add(request);
    profiles.removeWhere((item) => item.id == request.profileId);
  }

  @override
  Future<TunnelStatus> connect(ConnectRequest request) async {
    callOrder.add('connect');
    connectRequests.add(request);
    status = _copyStatus(
      status,
      state: TunnelState.connecting,
      profileId: request.profileId,
      carrier: CarrierKind.none,
    );
    return status;
  }

  @override
  Future<TunnelStatus> disconnect(DisconnectRequest request) async {
    callOrder.add('disconnect');
    disconnectRequests.add(request);
    status = _copyStatus(
      status,
      state: TunnelState.disconnecting,
      carrier: CarrierKind.http3WebTransport,
    );
    return status;
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

TunnelStatus _copyStatus(
  TunnelStatus source, {
  required TunnelState state,
  required CarrierKind carrier,
  String? profileId,
}) {
  return TunnelStatus(
    state: state,
    profileId: profileId ?? source.profileId,
    carrier: carrier,
    connectedAtUnixMs: source.connectedAtUnixMs,
    uploadBytesPerSecond: source.uploadBytesPerSecond,
    downloadBytesPerSecond: source.downloadBytesPerSecond,
    uploadTotalBytes: source.uploadTotalBytes,
    downloadTotalBytes: source.downloadTotalBytes,
    sequence: source.sequence + 1,
    lastError: source.lastError,
  );
}

ProfileSummary _copyProfile(ProfileSummary source, {required bool selected}) {
  return ProfileSummary(
    id: source.id,
    displayName: source.displayName,
    serverIdentity: source.serverIdentity,
    host: source.host,
    selected: selected,
    hasCredential: source.hasCredential,
    origin: source.origin,
    catalogManaged: source.catalogManaged,
    updatedAtUnixMs: source.updatedAtUnixMs,
  );
}
