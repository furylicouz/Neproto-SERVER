import Foundation
import NeProtoCore
@preconcurrency import NetworkExtension

enum IOSTunnelCoordinatorError: Error {
  case invalidBundleIdentifier
  case activeProfileConflict
  case managerUnavailable
}

@MainActor
protocol IOSTunnelManaging: AnyObject {
  var statusChanged: ((TunnelStatus) -> Void)? { get set }
  var runtimeHealth: IOSRuntimeHealth? { get }
  func connect(profile: ServerProfile, providerConfiguration: [String: Any], credentialReference: Data) async throws -> TunnelStatus
  func disconnect(operationID: String) async throws -> TunnelStatus
  func removeConfiguration(profileID: UUID) async throws
  func status(selectedProfileID: UUID?) async throws -> TunnelStatus
}

enum IOSVPNProtocolFactory {
  static func make(
    profile: ServerProfile,
    providerConfiguration: [String: Any],
    credentialReference: Data,
    providerBundleID: String
  ) -> NETunnelProviderProtocol {
    let tunnelProtocol = NETunnelProviderProtocol()
    tunnelProtocol.providerBundleIdentifier = providerBundleID
    let concreteServerAddress = profile.serverAddress?.trimmingCharacters(in: .whitespacesAndNewlines)
    tunnelProtocol.serverAddress = concreteServerAddress.flatMap { $0.isEmpty ? nil : $0 }
      ?? profile.serverIdentity
    tunnelProtocol.passwordReference = credentialReference
    tunnelProtocol.providerConfiguration = providerConfiguration
    tunnelProtocol.includeAllNetworks = true
    tunnelProtocol.enforceRoutes = false
    return tunnelProtocol
  }
}

/// Owns NetworkExtension configuration and status mapping for the Flutter
/// container. The Packet Tunnel extension remains a separate process and does
/// not contain a Flutter engine.
@MainActor
final class IOSTunnelCoordinator: IOSTunnelManaging {
  var statusChanged: ((TunnelStatus) -> Void)?
  private(set) var runtimeHealth: IOSRuntimeHealth?

  private var managers: [UUID: NETunnelProviderManager] = [:]
  private var activeProfileID: UUID?
  private var sequence: Int64 = 0
  private var observer: NSObjectProtocol?
	private let notificationCenter: NotificationCenter

  init(notificationCenter: NotificationCenter = .default) {
		self.notificationCenter = notificationCenter
    observer = notificationCenter.addObserver(
      forName: .NEVPNStatusDidChange,
      object: nil,
      queue: .main
    ) { [weak self] notification in
			MainActor.assumeIsolated { self?.handleStatusChange(notification) }
    }
  }

  deinit {
    if let observer {
			notificationCenter.removeObserver(observer)
    }
  }

  func connect(
    profile: ServerProfile,
    providerConfiguration: [String: Any],
    credentialReference: Data
  ) async throws -> TunnelStatus {
    runtimeHealth = nil
    try await reloadManagers()
    if let activeProfileID, activeProfileID != profile.id,
       let activeManager = managers[activeProfileID],
       Self.isActive(activeManager.connection.status) {
      throw IOSTunnelCoordinatorError.activeProfileConflict
    }
    guard let appBundleID = Bundle.main.bundleIdentifier,
          let providerBundleID = PacketTunnelBundleIdentifier.derive(from: appBundleID) else {
      throw IOSTunnelCoordinatorError.invalidBundleIdentifier
    }

    let manager = managers[profile.id] ?? NETunnelProviderManager()
    let tunnelProtocol = IOSVPNProtocolFactory.make(
      profile: profile,
      providerConfiguration: providerConfiguration,
      credentialReference: credentialReference,
      providerBundleID: providerBundleID
    )
    manager.localizedDescription = "NeProto — \(profile.name)"
    manager.protocolConfiguration = tunnelProtocol
    manager.isEnabled = true

    try await save(manager)
    try await load(manager)
    try manager.connection.startVPNTunnel()
    managers[profile.id] = manager
    activeProfileID = profile.id
    return snapshot(profileID: profile.id, manager: manager, overriding: .connecting)
  }

  func disconnect(operationID _: String) async throws -> TunnelStatus {
    guard let profileID = activeProfileID,
          let manager = managers[profileID] else {
      return snapshot(profileID: nil, manager: nil, overriding: .disconnected)
    }
    manager.connection.stopVPNTunnel()
    runtimeHealth = nil
    return snapshot(profileID: profileID, manager: manager, overriding: .disconnecting)
  }

  func removeConfiguration(profileID: UUID) async throws {
    try await reloadManagers()
    guard let manager = managers[profileID] else {
      return
    }
    if Self.isActive(manager.connection.status) {
      manager.connection.stopVPNTunnel()
    }
    try await remove(manager)
    managers.removeValue(forKey: profileID)
    if activeProfileID == profileID {
      activeProfileID = nil
      runtimeHealth = nil
    }
  }

  func status(selectedProfileID: UUID?) async throws -> TunnelStatus {
    try await reloadManagers()
    let profileID = activeProfileID ?? selectedProfileID
    let manager = profileID.flatMap { managers[$0] }
    let health = await requestRuntimeHealth(manager)
    runtimeHealth = health
    return snapshot(profileID: profileID, manager: manager, runtime: health)
  }

  private func reloadManagers() async throws {
    let loaded: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
      NETunnelProviderManager.loadAllFromPreferences { managers, error in
        if let error {
          continuation.resume(throwing: error)
        } else {
          continuation.resume(returning: managers ?? [])
        }
      }
    }
    var mapped: [UUID: NETunnelProviderManager] = [:]
    for manager in loaded {
      guard let tunnelProtocol = manager.protocolConfiguration as? NETunnelProviderProtocol,
            let rawID = tunnelProtocol.providerConfiguration?["profile_id"] as? String,
            let profileID = UUID(uuidString: rawID) else {
        continue
      }
      mapped[profileID] = manager
    }
    managers = mapped
    if let connected = mapped.first(where: { Self.isActive($0.value.connection.status) }) {
      activeProfileID = connected.key
    } else if let activeProfileID, mapped[activeProfileID] == nil {
      self.activeProfileID = nil
    }
  }

  private func handleStatusChange(_ notification: Notification) {
    guard let connection = notification.object as? NEVPNConnection,
          let entry = managers.first(where: { $0.value.connection === connection }) else {
      return
    }
    if connection.status == .connected || connection.status == .connecting || connection.status == .reasserting {
      activeProfileID = entry.key
    } else if connection.status == .disconnected || connection.status == .invalid,
              activeProfileID == entry.key {
      activeProfileID = nil
      runtimeHealth = nil
    }
    statusChanged?(snapshot(profileID: entry.key, manager: entry.value))
  }

  private func snapshot(
    profileID: UUID?,
    manager: NETunnelProviderManager?,
    overriding stateOverride: TunnelState? = nil,
    runtime: IOSRuntimeHealth? = nil
  ) -> TunnelStatus {
    sequence = sequence == Int64.max ? 1 : sequence + 1
    let status = manager?.connection.status ?? .disconnected
		let state = stateOverride ?? IOSVPNStatusMapper.state(status)
		let carrier = IOSVPNStatusMapper.carrier(state)
    let connectedAt = manager?.connection.connectedDate.map {
      Int64(max(0, $0.timeIntervalSince1970 * 1_000))
    } ?? 0
    return TunnelStatus(
      state: state,
      profileId: profileID?.uuidString.lowercased(),
      carrier: carrier,
      connectedAtUnixMs: connectedAt,
      uploadBytesPerSecond: runtime?.uploadBytesPerSecond ?? 0,
      downloadBytesPerSecond: runtime?.downloadBytesPerSecond ?? 0,
      uploadTotalBytes: runtime?.uploadTotalBytes ?? 0,
      downloadTotalBytes: runtime?.downloadTotalBytes ?? 0,
      sequence: sequence,
      lastError: nil
    )
  }

  private func requestRuntimeHealth(_ manager: NETunnelProviderManager?) async -> IOSRuntimeHealth? {
    guard let manager,
          manager.connection.status == .connected || manager.connection.status == .reasserting,
          let session = manager.connection as? NETunnelProviderSession else {
      return nil
    }
    return await withCheckedContinuation { continuation in
      let reply = IOSRuntimeHealthReply(continuation)
      DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 1) {
        reply.finish(nil)
      }
      do {
        try session.sendProviderMessage(Data("np2-runtime-snapshot".utf8)) { data in
          reply.finish(data.flatMap(IOSRuntimeHealth.decode))
        }
      } catch {
        reply.finish(nil)
      }
    }
  }
}

private final class IOSRuntimeHealthReply: @unchecked Sendable {
  private let lock = NSLock()
  private var continuation: CheckedContinuation<IOSRuntimeHealth?, Never>?

  init(_ continuation: CheckedContinuation<IOSRuntimeHealth?, Never>) {
    self.continuation = continuation
  }

  func finish(_ value: IOSRuntimeHealth?) {
    let owned: CheckedContinuation<IOSRuntimeHealth?, Never>? = lock.withLock {
      defer { continuation = nil }
      return continuation
    }
    owned?.resume(returning: value)
  }
}

struct IOSRuntimeHealth: Decodable, Equatable, Sendable {
  static let maximumSnapshotBytes = 16 * 1024

  let carrier: String
  let uploadBytesPerSecond: Int64
  let downloadBytesPerSecond: Int64
  let uploadTotalBytes: Int64
  let downloadTotalBytes: Int64
  let quicSmoothedRTTMS: Int64
  let quicPacketsSent: UInt64
  let quicPacketsLost: UInt64
  let quicBytesSent: UInt64
  let quicBytesLost: UInt64

  enum CodingKeys: String, CodingKey {
    case carrier
    case uploadBytesPerSecond = "upload_bytes_per_second"
    case downloadBytesPerSecond = "download_bytes_per_second"
    case uploadTotalBytes = "upload_total_bytes"
    case downloadTotalBytes = "download_total_bytes"
    case quicSmoothedRTTMS = "quic_smoothed_rtt_ms"
    case quicPacketsSent = "quic_packets_sent"
    case quicPacketsLost = "quic_packets_lost"
    case quicBytesSent = "quic_bytes_sent"
    case quicBytesLost = "quic_bytes_lost"
  }

  static func decode(_ data: Data) -> IOSRuntimeHealth? {
    guard !data.isEmpty, data.count <= maximumSnapshotBytes,
          let value = try? JSONDecoder().decode(IOSRuntimeHealth.self, from: data),
		  value.carrier == "https_websocket" || value.carrier == "http3_webtransport",
          value.uploadBytesPerSecond >= 0, value.downloadBytesPerSecond >= 0,
          value.uploadTotalBytes >= 0, value.downloadTotalBytes >= 0,
          value.quicSmoothedRTTMS >= 0, value.quicSmoothedRTTMS <= 10 * 60 * 1_000,
          value.quicPacketsLost <= value.quicPacketsSent,
          value.quicBytesLost <= value.quicBytesSent else {
      return nil
    }
    return value
  }

  var diagnosticMessage: String {
	if carrier == "https_websocket" {
	  return String(
		format: "HTTPS/TLS health: upload %lld B/s, download %lld B/s.",
		uploadBytesPerSecond,
		downloadBytesPerSecond
	  )
	}
    let lossPercent = quicPacketsSent == 0
      ? 0
      : Double(quicPacketsLost) * 100 / Double(quicPacketsSent)
    return String(
      format: "HTTP/3 health: RTT %lld ms, packet loss %llu/%llu (%.2f%%), upload %lld B/s, download %lld B/s.",
      quicSmoothedRTTMS,
      quicPacketsLost,
      quicPacketsSent,
      lossPercent,
      uploadBytesPerSecond,
      downloadBytesPerSecond
    )
  }
}

enum IOSVPNStatusMapper {
	static func state(_ status: NEVPNStatus) -> TunnelState {
    switch status {
    case .invalid, .disconnected: .disconnected
    case .connecting: .connecting
    case .connected: .connected
    case .reasserting: .reconnecting
    case .disconnecting: .disconnecting
    @unknown default: .unknown
    }
  }

	static func carrier(_ state: TunnelState) -> CarrierKind {
		switch state {
		case .connected, .reconnecting, .disconnecting: .httpsWebSocket
		default: .none
		}
	}
}

private extension IOSTunnelCoordinator {
	static func isActive(_ status: NEVPNStatus) -> Bool {
    switch status {
    case .connecting, .connected, .reasserting, .disconnecting: true
    default: false
    }
  }

  private func save(_ manager: NETunnelProviderManager) async throws {
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
      manager.saveToPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }

  private func load(_ manager: NETunnelProviderManager) async throws {
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
      manager.loadFromPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }

  private func remove(_ manager: NETunnelProviderManager) async throws {
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
      manager.removeFromPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }
}
