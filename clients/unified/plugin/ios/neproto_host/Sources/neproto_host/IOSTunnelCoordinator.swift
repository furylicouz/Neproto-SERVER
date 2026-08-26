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
  func connect(profile: ServerProfile, providerConfiguration: [String: Any], credentialReference: Data) async throws -> TunnelStatus
  func disconnect(operationID: String) async throws -> TunnelStatus
  func removeConfiguration(profileID: UUID) async throws
  func status(selectedProfileID: UUID?) async throws -> TunnelStatus
}

/// Owns NetworkExtension configuration and status mapping for the Flutter
/// container. The Packet Tunnel extension remains a separate process and does
/// not contain a Flutter engine.
@MainActor
final class IOSTunnelCoordinator: IOSTunnelManaging {
  var statusChanged: ((TunnelStatus) -> Void)?

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
    let tunnelProtocol = NETunnelProviderProtocol()
    tunnelProtocol.providerBundleIdentifier = providerBundleID
    tunnelProtocol.serverAddress = profile.serverIdentity
    tunnelProtocol.passwordReference = credentialReference
    tunnelProtocol.providerConfiguration = providerConfiguration
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
    }
  }

  func status(selectedProfileID: UUID?) async throws -> TunnelStatus {
    try await reloadManagers()
    let profileID = activeProfileID ?? selectedProfileID
    let manager = profileID.flatMap { managers[$0] }
    return snapshot(profileID: profileID, manager: manager)
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
    }
    statusChanged?(snapshot(profileID: entry.key, manager: entry.value))
  }

  private func snapshot(
    profileID: UUID?,
    manager: NETunnelProviderManager?,
    overriding stateOverride: TunnelState? = nil
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
      uploadBytesPerSecond: 0,
      downloadBytesPerSecond: 0,
      uploadTotalBytes: 0,
      downloadTotalBytes: 0,
      sequence: sequence,
      lastError: nil
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
		case .connected, .reconnecting, .disconnecting: .http3WebTransport
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
    try await withCheckedThrowingContinuation { continuation in
      manager.saveToPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }

  private func load(_ manager: NETunnelProviderManager) async throws {
    try await withCheckedThrowingContinuation { continuation in
      manager.loadFromPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }

  private func remove(_ manager: NETunnelProviderManager) async throws {
    try await withCheckedThrowingContinuation { continuation in
      manager.removeFromPreferences { error in
        if let error { continuation.resume(throwing: error) }
        else { continuation.resume() }
      }
    }
  }
}
