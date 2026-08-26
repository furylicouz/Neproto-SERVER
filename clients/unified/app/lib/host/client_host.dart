import 'package:neproto_host/neproto_host.dart';

abstract interface class ClientHost {
  Stream<TunnelStatus> get statusChanges;

  Future<HostCapabilities> getCapabilities(HostApiVersion requestedVersion);

  Future<List<ProfileSummary>> listProfiles();

  Future<ProfileSummary> importProfile(ImportProfileRequest request);

  Future<ProfileSummary> selectProfile(SelectProfileRequest request);

  Future<void> removeProfile(RemoveProfileRequest request);

  Future<TunnelStatus> connect(ConnectRequest request);

  Future<TunnelStatus> disconnect(DisconnectRequest request);

  Future<TunnelStatus> getStatus();

  Future<DiagnosticsSnapshot> getDiagnostics(DiagnosticsRequest request);

  void dispose();
}
