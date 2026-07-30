import Foundation
import NeProtoCore
@preconcurrency import NetworkExtension
import OSLog

struct LiveTrafficMetrics: Equatable, Sendable {
    var uploadBytesPerSecond: Int64 = 0
    var downloadBytesPerSecond: Int64 = 0
    var uploadTotalBytes: Int64 = 0
    var downloadTotalBytes: Int64 = 0
}

@MainActor
final class VPNService: ObservableObject {
    @Published private(set) var statuses: [UUID: NEVPNStatus] = [:]
    @Published private(set) var busyProfileIDs: Set<UUID> = []
    @Published private(set) var diagnosticLog: [String] = []
    @Published private(set) var liveTraffic: [UUID: LiveTrafficMetrics] = [:]
    @Published var lastError: String?

    private var managers: [UUID: NETunnelProviderManager] = [:]
    private var userInitiatedStops: Set<UUID> = []
    private var clusterCatalogSessionIDs: [UUID: UInt64] = [:]
    private var nextClusterCatalogSessionID: UInt64 = 0
    private let secretStore = KeychainSecretStore()
    private let logger = Logger(subsystem: "NeProto", category: "VPNService")

    func reload(completion: (@MainActor () -> Void)? = nil) {
        NETunnelProviderManager.loadAllFromPreferences { [weak self] loaded, error in
            let loadedBox = UncheckedSendableBox(value: loaded ?? [])
            DispatchQueue.main.async {
                guard let self else { return }
                if let error {
                    self.lastError = error.localizedDescription
                    return
                }
                var mapped: [UUID: NETunnelProviderManager] = [:]
                for manager in loadedBox.value {
                    guard let configuration = manager.protocolConfiguration as? NETunnelProviderProtocol,
                          let rawID = configuration.providerConfiguration?["profile_id"] as? String,
                          let id = UUID(uuidString: rawID) else { continue }
                    mapped[id] = manager
                }
                self.managers = mapped
                self.refreshStatuses()
                completion?()
            }
        }
    }

    func recordDiagnostic(_ message: String) {
        appendDiagnostic(message)
    }

    func toggle(profile: ServerProfile, clientRoutes: [ClusterRoute] = []) {
        guard !isBusy(profileID: profile.id) else { return }
        switch status(for: profile.id) {
        case .connected, .connecting, .reasserting:
            appendDiagnostic("Отключение запрошено пользователем: \(profile.name)")
            guard let connection = managers[profile.id]?.connection else { return }
            userInitiatedStops.insert(profile.id)
            connection.stopVPNTunnel()
            refreshStatuses()
        case .disconnecting:
            return
        default:
            guard profile.clusterAvailable else {
                lastError = VPNServiceError.clusterServerUnavailable.localizedDescription
                return
            }
            userInitiatedStops.remove(profile.id)
            connect(profile: profile, clientRoutes: clientRoutes)
        }
    }

    func removeConfiguration(profileID: UUID) {
        guard let manager = managers[profileID] else { return }
        clusterCatalogSessionIDs.removeValue(forKey: profileID)
        userInitiatedStops.insert(profileID)
        manager.connection.stopVPNTunnel()
        manager.removeFromPreferences { [weak self] error in
            DispatchQueue.main.async {
                if let error {
                    self?.lastError = error.localizedDescription
                }
                self?.reload()
            }
        }
    }

    func refreshStatuses() {
        statuses = managers.mapValues { $0.connection.status }
    }

    func handleStatusChange() {
        let previousStatuses = statuses
        refreshStatuses()

        for (profileID, manager) in managers {
            let previous = previousStatuses[profileID] ?? .disconnected
            let current = manager.connection.status
            if previous != current {
                appendDiagnostic("VPN: \(Self.statusName(previous)) → \(Self.statusName(current))")
            }
            if current == .connected, previous != .connected {
                userInitiatedStops.remove(profileID)
                clusterCatalogSessionIDs.removeValue(forKey: profileID)
                requestProviderDiagnostics(profileID: profileID)
            }
            if current == .disconnected || current == .invalid {
                clusterCatalogSessionIDs.removeValue(forKey: profileID)
            }
            guard current == .disconnected,
                  previous == .connecting || previous == .reasserting || previous == .connected ||
                  previous == .disconnecting else {
                continue
            }

            liveTraffic[profileID] = LiveTrafficMetrics()
            let wasUserInitiated = userInitiatedStops.remove(profileID) != nil
            guard VPNDisconnectPolicy.shouldReportError(wasUserInitiated: wasUserInitiated) else {
                appendDiagnostic("Отключено по запросу пользователя")
                statuses[profileID] = .disconnected
                continue
            }
            reportLastDisconnectError(profileID: profileID, connection: manager.connection, showFallback: true)
        }
    }

    func status(for profileID: UUID) -> NEVPNStatus {
        statuses[profileID] ?? .disconnected
    }

    func traffic(for profileID: UUID) -> LiveTrafficMetrics {
        liveTraffic[profileID] ?? LiveTrafficMetrics()
    }

    func connectedSince(profileID: UUID) -> Date? {
        guard status(for: profileID) == .connected else { return nil }
        return managers[profileID]?.connection.connectedDate
    }

    func clusterCatalogSessionID(profileID: UUID) -> UInt64? {
        guard status(for: profileID) == .connected else { return nil }
        if let sessionID = clusterCatalogSessionIDs[profileID] { return sessionID }
        nextClusterCatalogSessionID &+= 1
        if nextClusterCatalogSessionID == 0 { nextClusterCatalogSessionID = 1 }
        clusterCatalogSessionIDs[profileID] = nextClusterCatalogSessionID
        return nextClusterCatalogSessionID
    }

    func refreshLiveMetrics(profileID: UUID) {
        guard status(for: profileID) == .connected,
              let manager = managers[profileID],
              let session = manager.connection as? NETunnelProviderSession else {
            liveTraffic[profileID] = LiveTrafficMetrics()
            return
        }

        do {
            try session.sendProviderMessage(Data("np2-dashboard".utf8)) { [weak self] response in
                let responseBox = UncheckedSendableBox(value: response)
                DispatchQueue.main.async {
                    guard let self,
                          self.status(for: profileID) == .connected,
                          let data = responseBox.value,
                          let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                        return
                    }
                    self.liveTraffic[profileID] = LiveTrafficMetrics(
                        uploadBytesPerSecond: Self.int64(object["upload_bytes_per_second"]),
                        downloadBytesPerSecond: Self.int64(object["download_bytes_per_second"]),
                        uploadTotalBytes: Self.int64(object["upload_total_bytes"]),
                        downloadTotalBytes: Self.int64(object["download_total_bytes"])
                    )
                }
            }
        } catch {
            liveTraffic[profileID] = LiveTrafficMetrics()
        }
    }

    func isBusy(profileID: UUID) -> Bool {
        busyProfileIDs.contains(profileID)
    }

    var diagnosticsText: String {
        diagnosticLog.joined(separator: "\n")
    }

    func refreshProviderDiagnostics() {
        let connected = managers.filter { $0.value.connection.status == .connected }
        guard !connected.isEmpty else {
            appendDiagnostic("PacketTunnel недоступен: VPN не подключён")
            return
        }
        for profileID in connected.keys {
            requestProviderDiagnostics(profileID: profileID)
        }
    }

    func requestClusterCatalog(
        profileID: UUID,
        completion: @escaping @MainActor (Result<Data, Error>) -> Void
    ) {
        guard status(for: profileID) == .connected,
              let manager = managers[profileID],
              let session = manager.connection as? NETunnelProviderSession else {
            completion(.failure(VPNServiceError.packetTunnelNotConnected))
            return
        }
        do {
            try session.sendProviderMessage(Data("np2-cluster-catalog".utf8)) { response in
                let responseBox = UncheckedSendableBox(value: response)
                DispatchQueue.main.async {
                    guard let data = responseBox.value, !data.isEmpty else {
                        completion(.failure(VPNServiceError.emptyClusterCatalog))
                        return
                    }
                    completion(.success(data))
                }
            }
        } catch {
            completion(.failure(error))
        }
    }

    private func connect(profile: ServerProfile, clientRoutes: [ClusterRoute]) {
        lastError = nil
        busyProfileIDs.insert(profile.id)
        appendDiagnostic("Запуск NP/2: \(profile.serverIdentity)")
        do {
            guard let providerBundleIdentifier = PacketTunnelBundleIdentifier.derive(
                from: Bundle.main.bundleIdentifier
            ) else {
                throw VPNServiceError.missingApplicationBundleIdentifier
            }
            logger.notice("Connecting profile with provider=\(providerBundleIdentifier, privacy: .public)")
            let persistentReference = try secretStore.persistentReference(profileID: profile.id)
            let payload = try profile.providerPayload()
            let routeEncoder = JSONEncoder()
            routeEncoder.outputFormatting = [.sortedKeys]
            let routePayload = try routeEncoder.encode(clientRoutes.filter { $0.source == .client })
            loadFreshManager(profileID: profile.id) { [weak self] result in
                guard let self else { return }
                switch result {
                case let .success(manager):
                    self.saveAndStart(
                        manager: manager,
                        profile: profile,
                        persistentReference: persistentReference,
                        payload: payload,
                        routePayload: routePayload,
                        providerBundleIdentifier: providerBundleIdentifier,
                        retryStaleOnce: true
                    )
                case let .failure(error):
                    self.finish(profileID: profile.id, error: error)
                }
            }
        } catch {
            finish(profileID: profile.id, error: error)
        }
    }

    private func loadFreshManager(
        profileID: UUID,
        completion: @escaping @MainActor (Result<NETunnelProviderManager, Error>) -> Void
    ) {
        NETunnelProviderManager.loadAllFromPreferences { loaded, error in
            let result = UncheckedSendableBox(value: (loaded ?? [], error))
            DispatchQueue.main.async {
                if let error = result.value.1 {
                    completion(.failure(error))
                    return
                }
                let manager = result.value.0.first { manager in
                    guard let configuration = manager.protocolConfiguration as? NETunnelProviderProtocol,
                          let rawID = configuration.providerConfiguration?["profile_id"] as? String else {
                        return false
                    }
                    return UUID(uuidString: rawID) == profileID
                } ?? NETunnelProviderManager()
                completion(.success(manager))
            }
        }
    }

    private func saveAndStart(
        manager: NETunnelProviderManager,
        profile: ServerProfile,
        persistentReference: Data,
        payload: Data,
        routePayload: Data,
        providerBundleIdentifier: String,
        retryStaleOnce: Bool
    ) {
        if let existingProtocol = manager.protocolConfiguration as? NETunnelProviderProtocol,
           let existingProvider = existingProtocol.providerBundleIdentifier,
           existingProvider != providerBundleIdentifier {
            logger.notice(
                "Replacing VPN configuration with legacy provider=\(existingProvider, privacy: .public)"
            )
            manager.removeFromPreferences { [weak self] error in
                let errorBox = UncheckedSendableBox(value: error)
                DispatchQueue.main.async {
                    guard let self else { return }
                    if let error = errorBox.value {
                        self.finish(profileID: profile.id, error: error)
                        return
                    }
                    self.managers.removeValue(forKey: profile.id)
                    self.saveAndStart(
                        manager: NETunnelProviderManager(),
                        profile: profile,
                        persistentReference: persistentReference,
                        payload: payload,
                        routePayload: routePayload,
                        providerBundleIdentifier: providerBundleIdentifier,
                        retryStaleOnce: retryStaleOnce
                    )
                }
            }
            return
        }

        let tunnelProtocol = NETunnelProviderProtocol()
        tunnelProtocol.providerBundleIdentifier = providerBundleIdentifier
        tunnelProtocol.serverAddress = profile.serverIdentity
        tunnelProtocol.passwordReference = persistentReference
        tunnelProtocol.providerConfiguration = [
            "profile_id": profile.id.uuidString.lowercased(),
            "profile_payload": payload,
            "client_routes": routePayload,
        ]

        manager.localizedDescription = "NeProto — \(profile.name)"
        manager.protocolConfiguration = tunnelProtocol
        manager.isEnabled = true

        let managerBox = UncheckedSendableBox(value: manager)
        manager.saveToPreferences { [weak self] error in
            let errorBox = UncheckedSendableBox(value: error)
            DispatchQueue.main.async {
                guard let self else { return }
                if let error = errorBox.value {
                    let nsError = error as NSError
                    self.logger.error(
                        "Saving VPN configuration failed: domain=\(nsError.domain, privacy: .public) code=\(nsError.code)"
                    )
                    if retryStaleOnce, VPNConfigurationError.isStale(error) {
                        self.loadFreshManager(profileID: profile.id) { [weak self] result in
                            guard let self else { return }
                            switch result {
                            case let .success(freshManager):
                                self.saveAndStart(
                                    manager: freshManager,
                                    profile: profile,
                                    persistentReference: persistentReference,
                                    payload: payload,
                                    routePayload: routePayload,
                                    providerBundleIdentifier: providerBundleIdentifier,
                                    retryStaleOnce: false
                                )
                            case let .failure(loadError):
                                self.finish(profileID: profile.id, error: loadError)
                            }
                        }
                    } else {
                        self.finish(profileID: profile.id, error: error)
                    }
                    return
                }
                self.logger.notice("VPN configuration saved")
                self.appendDiagnostic("VPN-конфигурация сохранена")
                self.reloadAndStart(manager: managerBox.value, profileID: profile.id)
            }
        }
    }

    private func reloadAndStart(manager: NETunnelProviderManager, profileID: UUID) {
        let managerBox = UncheckedSendableBox(value: manager)
        manager.loadFromPreferences { [weak self] error in
            let errorBox = UncheckedSendableBox(value: error)
            DispatchQueue.main.async {
                guard let self else { return }
                if let error = errorBox.value {
                    self.finish(profileID: profileID, error: error)
                    return
                }
                do {
                    try managerBox.value.connection.startVPNTunnel()
                    self.managers[profileID] = managerBox.value
                    self.statuses[profileID] = .connecting
                    self.finish(profileID: profileID)
                    self.logger.notice("startVPNTunnel request accepted")
                    self.appendDiagnostic("Запрос startVPNTunnel принят iOS")
                    self.verifyStarted(profileID: profileID, manager: managerBox.value)
                } catch {
                    self.finish(profileID: profileID, error: error)
                }
            }
        }
    }

    private func verifyStarted(profileID: UUID, manager: NETunnelProviderManager) {
        let managerBox = UncheckedSendableBox(value: manager)
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            guard let self, managerBox.value.connection.status == .disconnected else { return }
            self.logger.error("Packet Tunnel remained disconnected after start request")
            self.reportLastDisconnectError(
                profileID: profileID,
                connection: managerBox.value.connection,
                showFallback: true
            )
        }
    }

    private func reportLastDisconnectError(
        profileID: UUID,
        connection: NEVPNConnection,
        showFallback: Bool
    ) {
        connection.fetchLastDisconnectError { [weak self] error in
            let errorBox = UncheckedSendableBox(value: error)
            DispatchQueue.main.async {
                guard let self, self.status(for: profileID) == .disconnected else { return }
                if let error = errorBox.value {
                    let nsError = error as NSError
                    self.logger.error(
                        "VPN disconnected: domain=\(nsError.domain, privacy: .public) code=\(nsError.code)"
                    )
                    self.lastError = "VPN отключён: \(nsError.domain) (\(nsError.code)): \(nsError.localizedDescription)"
                    self.appendDiagnostic(
                        "Отключение: \(nsError.domain) (\(nsError.code)): \(nsError.localizedDescription)"
                    )
                } else if showFallback {
                    self.lastError = "Packet Tunnel завершил работу без системной причины. Откройте «Журнал» для диагностики."
                    self.appendDiagnostic("Отключение без системной причины")
                }
                self.statuses[profileID] = .disconnected
            }
        }
    }

    private func finish(profileID: UUID, error: Error? = nil) {
        busyProfileIDs.remove(profileID)
        if let error {
            appendDiagnostic("Ошибка: \(error.localizedDescription)")
            lastError = error.localizedDescription
        }
    }

    private func requestProviderDiagnostics(profileID: UUID) {
        guard let manager = managers[profileID],
              let session = manager.connection as? NETunnelProviderSession else {
            appendDiagnostic("Нет NETunnelProviderSession для диагностики")
            return
        }
        do {
            try session.sendProviderMessage(Data("np2-diagnostics".utf8)) { [weak self] response in
                let responseBox = UncheckedSendableBox(value: response)
                DispatchQueue.main.async {
                    guard let self else { return }
                    guard let data = responseBox.value,
                          let rawObject = try? JSONSerialization.jsonObject(with: data),
                          let object = rawObject as? [String: Any] else {
                        self.appendDiagnostic("PacketTunnel не вернул snapshot")
                        return
                    }
                    let fields = [
                        "state", "version", "carrier", "data_plane", "cell_encryption", "server_routes", "last_error",
                        "download_bytes_per_second", "upload_bytes_per_second", "download_total_bytes", "upload_total_bytes",
                        "udp_mode", "quic_fallbacks",
                        "carrier_pool_target", "carrier_pool_healthy", "carrier_pool_assignments",
                        "carrier_pool_scale_ups", "carrier_pool_failures",
                        "dns_attribution_queries", "dns_attribution_responses", "dns_attribution_hits",
                        "dns_attribution_misses", "dns_attribution_cached",
                        "first_flight_domain_hits", "first_flight_fallbacks",
                        "network_changes", "reconnects", "migrations",
                    ].compactMap { key -> String? in
                        guard let value = object[key] else { return nil }
                        let rendered = String(describing: value)
                        return rendered.isEmpty ? nil : "\(key)=\(rendered)"
                    }
                    self.appendDiagnostic("PacketTunnel: \(fields.joined(separator: " "))")
                }
            }
        } catch {
            appendDiagnostic("Запрос PacketTunnel: \(error.localizedDescription)")
        }
    }

    private func appendDiagnostic(_ message: String) {
        let timestamp = Date.now.formatted(date: .omitted, time: .standard)
        diagnosticLog.append("[\(timestamp)] \(message)")
        if diagnosticLog.count > 80 {
            diagnosticLog.removeFirst(diagnosticLog.count - 80)
        }
    }

    private static func statusName(_ status: NEVPNStatus) -> String {
        switch status {
        case .invalid: "invalid"
        case .disconnected: "disconnected"
        case .connecting: "connecting"
        case .connected: "connected"
        case .reasserting: "reasserting"
        case .disconnecting: "disconnecting"
        @unknown default: "unknown"
        }
    }

    private static func int64(_ value: Any?) -> Int64 {
        guard let number = value as? NSNumber else { return 0 }
        return max(0, number.int64Value)
    }
}

private struct UncheckedSendableBox<Value>: @unchecked Sendable {
    let value: Value
}

private enum VPNServiceError: LocalizedError {
    case missingApplicationBundleIdentifier
    case clusterServerUnavailable
    case packetTunnelNotConnected
    case emptyClusterCatalog

    var errorDescription: String? {
        switch self {
        case .missingApplicationBundleIdentifier:
            "Не удалось определить bundle identifier приложения."
        case .clusterServerUnavailable:
            "Сервер временно отключён администратором кластера NP/2."
        case .packetTunnelNotConnected:
            "Для синхронизации кластера сначала подключитесь к NP/2."
        case .emptyClusterCatalog:
            "Packet Tunnel не вернул каталог кластера NP/2."
        }
    }
}
