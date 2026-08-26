import 'package:pigeon/pigeon.dart';

@ConfigurePigeon(
  PigeonOptions(
    dartOut: 'lib/src/generated/client_host_api.g.dart',
    dartOptions: DartOptions(),
    swiftOut: 'ios/neproto_host/Sources/neproto_host/ClientHostApi.g.swift',
    swiftOptions: SwiftOptions(),
    cppHeaderOut: 'windows/include/neproto_host/client_host_api.g.h',
    cppSourceOut: 'windows/client_host_api.g.cpp',
    cppOptions: CppOptions(namespace: 'neproto_host'),
    dartPackageName: 'neproto_host',
  ),
)
enum HostPlatform { unknown, windows, ios }

enum ProfileOrigin { unknown, imported, cluster }

enum TunnelState {
  unknown,
  disconnected,
  connecting,
  connected,
  reconnecting,
  disconnecting,
  failed,
}

enum CarrierKind { unknown, none, http3WebTransport }

enum HostErrorCode {
  unknown,
  hostUnavailable,
  unsupportedApiVersion,
  invalidProfile,
  credentialUnavailable,
  noSafeUplink,
  dnsFailed,
  udpUnreachable,
  tlsFailed,
  http3Timeout,
  np2AuthFailed,
  tunSetupFailed,
  cancelled,
  internalFailure,
}

enum ErrorStage {
  unknown,
  hostIpc,
  hostNegotiation,
  profileValidation,
  credentialLoad,
  dnsResolution,
  endpointRoute,
  quicHandshake,
  tlsHandshake,
  webTransportConnect,
  np2Authentication,
  tunSetup,
  packetForwarding,
}

enum DiagnosticLevel { unknown, info, warning, error }

class HostApiVersion {
  HostApiVersion({required this.major, required this.minor});

  int major;
  int minor;
}

class HostCapabilities {
  HostCapabilities({
    required this.apiVersion,
    required this.platform,
    required this.appVersion,
    required this.hostVersion,
    required this.coreVersion,
    required this.supportsHttp3WebTransport,
  });

  HostApiVersion apiVersion;
  HostPlatform platform;
  String appVersion;
  String hostVersion;
  String coreVersion;
  bool supportsHttp3WebTransport;
}

class ProfileSummary {
  ProfileSummary({
    required this.id,
    required this.displayName,
    required this.serverIdentity,
    required this.host,
    required this.selected,
    required this.hasCredential,
    required this.origin,
    required this.catalogManaged,
    required this.updatedAtUnixMs,
  });

  String id;
  String displayName;
  String serverIdentity;
  String host;
  bool selected;
  bool hasCredential;
  ProfileOrigin origin;
  bool catalogManaged;
  int updatedAtUnixMs;
}

class ImportProfileRequest {
  ImportProfileRequest({
    required this.onboardingValue,
    required this.operationId,
  });

  String onboardingValue;
  String operationId;
}

class SelectProfileRequest {
  SelectProfileRequest({required this.profileId, required this.operationId});

  String profileId;
  String operationId;
}

class RemoveProfileRequest {
  RemoveProfileRequest({
    required this.profileId,
    required this.force,
    required this.operationId,
  });

  String profileId;
  bool force;
  String operationId;
}

class ConnectRequest {
  ConnectRequest({required this.profileId, required this.operationId});

  String profileId;
  String operationId;
}

class DisconnectRequest {
  DisconnectRequest({required this.operationId});

  String operationId;
}

class HostError {
  HostError({
    required this.code,
    required this.stage,
    required this.message,
    required this.retryable,
    required this.operationId,
  });

  HostErrorCode code;
  ErrorStage stage;
  String message;
  bool retryable;
  String operationId;
}

class TunnelStatus {
  TunnelStatus({
    required this.state,
    required this.profileId,
    required this.carrier,
    required this.connectedAtUnixMs,
    required this.uploadBytesPerSecond,
    required this.downloadBytesPerSecond,
    required this.uploadTotalBytes,
    required this.downloadTotalBytes,
    required this.sequence,
    required this.lastError,
  });

  TunnelState state;
  String? profileId;
  CarrierKind carrier;
  int connectedAtUnixMs;
  int uploadBytesPerSecond;
  int downloadBytesPerSecond;
  int uploadTotalBytes;
  int downloadTotalBytes;
  int sequence;
  HostError? lastError;
}

class DiagnosticsRequest {
  DiagnosticsRequest({required this.limit});

  int limit;
}

class DiagnosticEvent {
  DiagnosticEvent({
    required this.unixMs,
    required this.level,
    required this.stage,
    required this.code,
    required this.message,
    required this.operationId,
    required this.sequence,
  });

  int unixMs;
  DiagnosticLevel level;
  ErrorStage stage;
  HostErrorCode? code;
  String message;
  String operationId;
  int sequence;
}

class DiagnosticsSnapshot {
  DiagnosticsSnapshot({
    required this.appVersion,
    required this.hostVersion,
    required this.coreVersion,
    required this.carrierPolicy,
    required this.currentCarrier,
    required this.reconnectCount,
    required this.events,
  });

  String appVersion;
  String hostVersion;
  String coreVersion;
  String carrierPolicy;
  CarrierKind currentCarrier;
  int reconnectCount;
  List<DiagnosticEvent> events;
}

@HostApi()
abstract class ClientHostApi {
  @async
  HostCapabilities getCapabilities(HostApiVersion requestedVersion);

  @async
  List<ProfileSummary> listProfiles();

  @async
  ProfileSummary importProfile(ImportProfileRequest request);

  @async
  ProfileSummary selectProfile(SelectProfileRequest request);

  @async
  void removeProfile(RemoveProfileRequest request);

  @async
  TunnelStatus connect(ConnectRequest request);

  @async
  TunnelStatus disconnect(DisconnectRequest request);

  @async
  TunnelStatus getStatus();

  @async
  DiagnosticsSnapshot getDiagnostics(DiagnosticsRequest request);
}

@FlutterApi()
abstract class ClientHostFlutterApi {
  void statusChanged(TunnelStatus status);
}
