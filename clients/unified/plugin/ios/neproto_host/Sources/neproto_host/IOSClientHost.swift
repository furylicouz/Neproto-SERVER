#if os(iOS)
import Flutter
#elseif os(macOS)
import FlutterMacOS
#endif
import Foundation
import NeProtoCore

struct IOSImportFailure {
  let pigeonCode: String
  let diagnosticCode: HostErrorCode
  let stage: ErrorStage
  let message: String
}

@MainActor
final class IOSClientHost: @preconcurrency ClientHostApi {
  static let apiVersion = HostApiVersion(major: 1, minor: 0)

  private let profiles: IOSProfileRepository
  private let tunnel: any IOSTunnelManaging
  private let flutter: ClientHostFlutterApi
  private var diagnostics: [DiagnosticEvent] = []
  private var diagnosticSequence: Int64 = 0

  init(
    binaryMessenger: FlutterBinaryMessenger,
    profiles: IOSProfileRepository? = nil,
    tunnel: (any IOSTunnelManaging)? = nil
  ) {
    self.profiles = profiles ?? IOSProfileRepository()
    self.tunnel = tunnel ?? IOSTunnelCoordinator()
    flutter = ClientHostFlutterApi(binaryMessenger: binaryMessenger)
    self.tunnel.statusChanged = { [weak self] status in
      Task { @MainActor [weak self] in
        guard let self else { return }
        self.record(level: .info, stage: .unknown, code: nil, message: "Tunnel state changed.", operationID: "status-change")
        try? await self.flutter.statusChanged(status: status)
      }
    }
  }

  func getCapabilities(requestedVersion: HostApiVersion) async throws -> HostCapabilities {
    guard requestedVersion.major == Self.apiVersion.major, requestedVersion.minor >= 0 else {
      throw Self.pigeonError(
        code: "UNSUPPORTED_API_VERSION",
        message: "This native host does not support the requested API version."
      )
    }
    let version = Self.bundleVersion()
    return HostCapabilities(
      apiVersion: Self.apiVersion,
      platform: .ios,
      appVersion: version,
      hostVersion: version,
      coreVersion: version,
      supportsHttp3WebTransport: true
    )
  }

  func listProfiles() async throws -> [ProfileSummary] {
    profiles.summaries()
  }

  func importProfile(request: ImportProfileRequest) async throws -> ProfileSummary {
    try Self.validateOperationID(request.operationId)
    do {
      let result = try profiles.importOnboarding(request.onboardingValue)
      record(level: .info, stage: .profileValidation, code: nil, message: "Profile imported.", operationID: request.operationId)
      return result
    } catch {
      let failure = Self.importFailure(for: error)
      record(
        level: .error,
        stage: failure.stage,
        code: failure.diagnosticCode,
        message: failure.message,
        operationID: request.operationId
      )
      throw Self.pigeonError(code: failure.pigeonCode, message: failure.message)
    }
  }

  func selectProfile(request: SelectProfileRequest) async throws -> ProfileSummary {
    try Self.validateOperationID(request.operationId)
    do {
      return try profiles.select(rawID: request.profileId)
    } catch {
      throw Self.pigeonError(code: "INVALID_PROFILE", message: "The selected profile does not exist.")
    }
  }

  func removeProfile(request: RemoveProfileRequest) async throws {
    try Self.validateOperationID(request.operationId)
    do {
      let profile = try profiles.profile(id: request.profileId)
      let status = try await tunnel.status(selectedProfileID: profile.id)
      if status.profileId == request.profileId,
         [.connecting, .connected, .reconnecting, .disconnecting].contains(status.state) {
        guard request.force else {
          throw Self.pigeonError(code: "HOST_UNAVAILABLE", message: "Disconnect the active profile before removing it.")
        }
        try await tunnel.removeConfiguration(profileID: profile.id)
      } else {
        try await tunnel.removeConfiguration(profileID: profile.id)
      }
      try profiles.remove(rawID: request.profileId)
      record(level: .info, stage: .profileValidation, code: nil, message: "Profile removed.", operationID: request.operationId)
    } catch let error as PigeonError {
      throw error
    } catch {
      throw Self.pigeonError(code: "INVALID_PROFILE", message: "The profile could not be removed.")
    }
  }

  func connect(request: ConnectRequest) async throws -> TunnelStatus {
    try Self.validateOperationID(request.operationId)
    do {
      let profile = try profiles.profile(id: request.profileId)
      let credential = try profiles.persistentCredentialReference(profileID: profile.id)
      let configuration = try profiles.strictProviderConfiguration(profile: profile)
      record(level: .info, stage: .tlsHandshake, code: nil, message: "Connecting with HTTPS/TLS WebSocket.", operationID: request.operationId)
      return try await tunnel.connect(
        profile: profile,
        providerConfiguration: configuration,
        credentialReference: credential
      )
    } catch let error as PigeonError {
      throw error
    } catch is KeychainSecretStoreError {
      record(level: .error, stage: .credentialLoad, code: .credentialUnavailable, message: "Credential is unavailable.", operationID: request.operationId)
      throw Self.pigeonError(code: "CREDENTIAL_UNAVAILABLE", message: "The profile credential is unavailable.")
    } catch is ProfileValidationError {
      record(level: .error, stage: .profileValidation, code: .invalidProfile, message: "The profile cannot use strict HTTPS/TLS.", operationID: request.operationId)
      throw Self.pigeonError(code: "INVALID_PROFILE", message: "The profile does not contain a valid HTTPS endpoint.")
    } catch {
      record(level: .error, stage: .hostIpc, code: .hostUnavailable, message: "The VPN request failed.", operationID: request.operationId)
      throw Self.pigeonError(code: "HOST_UNAVAILABLE", message: "The iOS VPN host rejected the request.")
    }
  }

  func disconnect(request: DisconnectRequest) async throws -> TunnelStatus {
    try Self.validateOperationID(request.operationId)
    do {
      record(level: .info, stage: .packetForwarding, code: nil, message: "Disconnect requested.", operationID: request.operationId)
      return try await tunnel.disconnect(operationID: request.operationId)
    } catch {
      throw Self.pigeonError(code: "HOST_UNAVAILABLE", message: "The iOS VPN host rejected the request.")
    }
  }

  func getStatus() async throws -> TunnelStatus {
    try await tunnel.status(selectedProfileID: profiles.selectedProfileID)
  }

  func getDiagnostics(request: DiagnosticsRequest) async throws -> DiagnosticsSnapshot {
    guard request.limit >= 1, request.limit <= 200 else {
      throw Self.pigeonError(code: "INVALID_PROFILE", message: "The diagnostics limit is invalid.")
    }
    let status = try await getStatus()
    var events = diagnostics
    if let health = tunnel.runtimeHealth {
      let runtimeSequence = diagnosticSequence == Int64.max ? 1 : diagnosticSequence + 1
      events.append(DiagnosticEvent(
        unixMs: Int64(Date().timeIntervalSince1970 * 1_000),
        level: .info,
        stage: .tlsHandshake,
        code: nil,
        message: health.diagnosticMessage,
        operationId: "runtime-health",
        sequence: runtimeSequence
      ))
    }
    return DiagnosticsSnapshot(
      appVersion: Self.bundleVersion(),
      hostVersion: Self.bundleVersion(),
      coreVersion: Self.bundleVersion(),
      carrierPolicy: "https-only",
      currentCarrier: status.carrier,
      reconnectCount: 0,
      events: Array(events.suffix(Int(request.limit)))
    )
  }

  private func record(
    level: DiagnosticLevel,
    stage: ErrorStage,
    code: HostErrorCode?,
    message: String,
    operationID: String
  ) {
    diagnosticSequence = diagnosticSequence == Int64.max ? 1 : diagnosticSequence + 1
    diagnostics.append(DiagnosticEvent(
      unixMs: Int64(Date().timeIntervalSince1970 * 1_000),
      level: level,
      stage: stage,
      code: code,
      message: message,
      operationId: operationID,
      sequence: diagnosticSequence
    ))
    if diagnostics.count > 200 {
      diagnostics.removeFirst(diagnostics.count - 200)
    }
  }

  private static func validateOperationID(_ value: String) throws {
    guard !value.isEmpty, value.utf8.count <= 64,
          value.utf8.allSatisfy({ $0 >= 0x21 && $0 <= 0x7e }) else {
      throw pigeonError(code: "INVALID_PROFILE", message: "The operation identifier is invalid.")
    }
  }

  static func importFailure(for error: Error) -> IOSImportFailure {
    if error is OnboardingProfileError || error is OnboardingImportResolutionError {
      return IOSImportFailure(
        pigeonCode: "INVALID_PROFILE",
        diagnosticCode: .invalidProfile,
        stage: .profileValidation,
        message: "The NP/2 onboarding value failed native validation."
      )
    }
    if error is KeychainSecretStoreError {
      return IOSImportFailure(
        pigeonCode: "CREDENTIAL_UNAVAILABLE",
        diagnosticCode: .credentialUnavailable,
        stage: .credentialLoad,
        message: "The profile credential could not be saved in Keychain."
      )
    }
    return IOSImportFailure(
      pigeonCode: "INTERNAL",
      diagnosticCode: .internalFailure,
      stage: .hostIpc,
      message: "The native profile store rejected the import."
    )
  }

  private static func pigeonError(code: String, message: String) -> PigeonError {
    PigeonError(code: code, message: message, details: nil)
  }

  private static func bundleVersion() -> String {
    (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "0.1.0"
  }
}
