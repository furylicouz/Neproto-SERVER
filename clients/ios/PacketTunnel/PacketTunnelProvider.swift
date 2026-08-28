import Foundation
import NeProtoCore
import Network
@preconcurrency import NetworkExtension
import NP2Mobile
import OSLog

final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    private let runtimeQueue = DispatchQueue(label: "com.neproto.packet-tunnel.runtime", qos: .userInitiated)
    private let migrationQueue = DispatchQueue(label: "com.neproto.packet-tunnel.migration", qos: .utility)
    private let shutdownQueue = DispatchQueue(label: "com.neproto.packet-tunnel.shutdown", qos: .utility)
    private let stateLock = NSLock()
    private let logger = Logger(subsystem: "NeProto", category: "StrictHTTPSPacketTunnel")

    private var core: Np2mobileClientCore?
    private var runtimeMonitor: DispatchSourceTimer?
    private var pathMonitor: NWPathMonitor?
    private var lastPathSignature: String?
    private var starting = false
    private var stopping = false
    private var migrationGate = StrictNetworkMigrationGate()

    override func startTunnel(
        options _: [String: NSObject]? = nil,
        completionHandler: @escaping (Error?) -> Void
    ) {
        let completion = TunnelStartCompletion(completionHandler)
		let accepted = stateLock.withLock {
			guard !starting, !stopping, core == nil else { return false }
			starting = true
			return true
		}
		guard accepted else {
			completion.call(Self.xpcSafeError(TunnelProviderError.alreadyActive))
			return
		}
        runtimeQueue.async { [weak self] in
            self?.startStrictHTTPSTunnel(completion: completion)
                ?? completion.call(Self.xpcSafeError(TunnelProviderError.providerReleased))
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        logger.notice("Stopping strict HTTPS Packet Tunnel, reason=\(reason.rawValue)")
        let completion = TunnelStopCompletion(completionHandler)
        let owned: Np2mobileClientCore? = stateLock.withLock {
            stopping = true
            runtimeMonitor?.cancel()
            runtimeMonitor = nil
            pathMonitor?.cancel()
            pathMonitor = nil
            lastPathSignature = nil
			migrationGate.reset()
            let result = core
            core = nil
            return result
        }
        shutdownQueue.async {
            if let owned {
                do {
                    try owned.close()
                } catch {
                    self.logMobileFailure("Closing strict HTTPS ClientCore failed", error: error as NSError)
                }
            }
			self.stateLock.withLock {
				self.starting = false
				self.stopping = false
			}
            completion.call()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        guard messageData.count <= 256 else {
            completionHandler?(nil)
            return
        }
        let activeCore = stateLock.withLock { core }
        guard let activeCore else {
            completionHandler?(Data(#"{"state":"disconnected","carrier":"none"}"#.utf8))
            return
        }
        if String(data: messageData, encoding: .utf8) == "np2-cluster-catalog" {
            var catalogError: NSError?
            let catalog = activeCore.catalogJSON(&catalogError)
            if catalogError != nil || catalog.utf8.count > 256 * 1024 {
                logMobileFailure("Cluster catalog request failed", error: catalogError)
                completionHandler?(nil)
                return
            }
            completionHandler?(Data(catalog.utf8))
            return
        }
        completionHandler?(Self.providerSnapshot(for: activeCore))
    }

    private func startStrictHTTPSTunnel(completion: TunnelStartCompletion) {
        var createdCore: Np2mobileClientCore?
        do {
            let tunnelProtocol = try requireTunnelProtocol()
            guard let persistentReference = tunnelProtocol.passwordReference else {
                throw TunnelProviderError.missingSecretReference
            }
            let bootstrap = try StrictHTTPSPacketTunnelBootstrap(
                providerConfiguration: tunnelProtocol.providerConfiguration ?? [:],
                credentialReference: persistentReference
            )
            let secret = try KeychainSecretStore().read(persistentReference: bootstrap.credentialReference)
            try bootstrap.profile.validate(secret: secret)

            var mobileError: NSError?
            guard let newCore = Np2mobileNewStrictHTTPSClientCore(&mobileError) else {
                throw mobileError ?? TunnelProviderError.clientCoreUnavailable
            }
            createdCore = newCore
            guard stateLock.withLock({
				guard starting, !stopping, core == nil else { return false }
                core = newCore
                return true
            }) else {
                throw TunnelProviderError.stopping
            }
            try newCore.setClientRoutesJSON(bootstrap.clientRoutesJSON)
            try newCore.connect(
                bootstrap.clientConfigurationJSON,
                secret: secret,
                operationID: Self.operationID(prefix: "ios-start"),
                profileID: bootstrap.profileID
            )
            let exclusions = try ServerRouteExclusions(newCore.serverAddresses())
            guard !stateLock.withLock({ stopping }) else {
                throw TunnelProviderError.stopping
            }

            setTunnelNetworkSettings(Self.networkSettings(excluding: exclusions)) { [weak self] settingsError in
                guard let self else {
                    try? newCore.close()
                    completion.call(Self.xpcSafeError(TunnelProviderError.providerReleased))
                    return
                }
                self.runtimeQueue.async {
                    do {
                        if let settingsError { throw settingsError }
                        guard !self.stateLock.withLock({ self.stopping }) else {
                            throw TunnelProviderError.stopping
                        }
                        let descriptor = neproto_duplicate_tunnel_file_descriptor()
                        guard descriptor >= 0 else {
                            throw TunnelProviderError.missingTunnelFileDescriptor
                        }
                        try newCore.attachPacketTunnel(Int64(descriptor), mtu: 1_500)
						guard self.stateLock.withLock({
							guard self.core === newCore, !self.stopping else { return false }
							self.starting = false
							return true
						}) else {
							throw TunnelProviderError.stopping
						}
                        self.startRuntimeMonitor(for: newCore)
                        self.startNetworkPathMonitor(for: newCore)
						guard self.stateLock.withLock({ self.core === newCore && !self.stopping }) else {
							throw TunnelProviderError.stopping
						}
                        self.logger.notice("Strict HTTPS WebSocket Packet Tunnel is connected")
                        completion.call(nil)
                    } catch {
                        self.finishStartFailure(core: newCore, error: error, completion: completion)
                    }
                }
            }
        } catch {
            finishStartFailure(core: createdCore, error: error, completion: completion)
        }
    }

    private func finishStartFailure(
        core failedCore: Np2mobileClientCore?,
        error: Error,
        completion: TunnelStartCompletion
    ) {
		stateLock.withLock { starting = false }
        if let failedCore {
            stateLock.withLock {
                if core === failedCore { core = nil }
            }
            try? failedCore.close()
        }
        let safeError = Self.xpcSafeError(error)
        logger.error("Strict HTTPS Packet Tunnel start failed: domain=\(safeError.domain, privacy: .public) code=\(safeError.code)")
        completion.call(safeError)
    }

    private func startRuntimeMonitor(for expectedCore: Np2mobileClientCore) {
        let timer = DispatchSource.makeTimerSource(queue: runtimeQueue)
        timer.schedule(deadline: .now() + 1, repeating: 1)
        timer.setEventHandler { [weak self, weak expectedCore] in
            guard let self, let expectedCore,
                  self.stateLock.withLock({ self.core === expectedCore && !self.stopping }) else { return }
            guard let data = expectedCore.snapshotJSON().data(using: .utf8),
                  let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let state = object["state"] as? String,
                  state == "failed" || state == "disconnected" else { return }
            self.terminateAfterRuntimeFailure(expectedCore, error: TunnelProviderError.runtimeStopped)
        }
		timer.resume()
        stateLock.withLock {
			guard core === expectedCore, !stopping else {
				timer.cancel()
				return
			}
            runtimeMonitor?.cancel()
            runtimeMonitor = timer
        }
    }

    private func startNetworkPathMonitor(for expectedCore: Np2mobileClientCore) {
        let monitor = NWPathMonitor()
        let coreReference = SendableClientCoreReference(expectedCore)
        monitor.pathUpdateHandler = { [weak self, coreReference] path in
            guard let self else { return }
            let expectedCore = coreReference.value
            let signature = Self.networkPathSignature(path)
            let changed = self.stateLock.withLock {
                guard self.core === expectedCore, !self.stopping else { return false }
                defer { self.lastPathSignature = signature }
				guard self.lastPathSignature.map({ $0 != signature }) ?? false else { return false }
				return self.migrationGate.pathChanged()
            }
            guard changed else { return }
			self.scheduleNetworkMigration(for: expectedCore)
        }
		monitor.start(queue: runtimeQueue)
        stateLock.withLock {
			guard core === expectedCore, !stopping else {
				monitor.cancel()
				return
			}
            pathMonitor?.cancel()
            pathMonitor = monitor
            lastPathSignature = nil
        }
    }

	private func scheduleNetworkMigration(for expectedCore: Np2mobileClientCore) {
		migrationQueue.async { [weak self, weak expectedCore] in
			guard let self, let expectedCore else { return }
			guard self.stateLock.withLock({ self.core === expectedCore && !self.stopping }) else {
				self.stateLock.withLock {
					self.migrationGate.reset()
				}
				return
			}
			self.reasserting = true
			let migrationError: NSError?
			do {
				try expectedCore.networkChanged(Self.operationID(prefix: "ios-network"))
				migrationError = nil
			} catch {
				migrationError = error as NSError
			}
			self.reasserting = false
			if let migrationError {
				self.logMobileFailure("Strict HTTPS reconnect exhausted", error: migrationError)
				self.terminateAfterRuntimeFailure(
					expectedCore,
					error: migrationError
				)
				return
			}
			self.logger.notice("Network transition retained strict HTTPS WebSocket")
			let repeatMigration = self.stateLock.withLock {
				guard self.core === expectedCore, !self.stopping else {
					self.migrationGate.reset()
					return false
				}
				return self.migrationGate.completed()
			}
			if repeatMigration {
				self.scheduleNetworkMigration(for: expectedCore)
			}
		}
	}

    private func terminateAfterRuntimeFailure(_ failedCore: Np2mobileClientCore, error: Error) {
        let shouldTerminate = stateLock.withLock {
            guard core === failedCore, !stopping else { return false }
            stopping = true
            core = nil
            runtimeMonitor?.cancel()
            runtimeMonitor = nil
            pathMonitor?.cancel()
            pathMonitor = nil
			migrationGate.reset()
            return true
        }
        guard shouldTerminate else { return }
        try? failedCore.close()
        cancelTunnelWithError(Self.xpcSafeError(error))
    }

    private func requireTunnelProtocol() throws -> NETunnelProviderProtocol {
        guard let tunnelProtocol = protocolConfiguration as? NETunnelProviderProtocol else {
            throw TunnelProviderError.invalidConfiguration
        }
        return tunnelProtocol
    }

    private func logMobileFailure(_ message: StaticString, error: NSError?) {
        if let error {
            logger.error("\(message): domain=\(error.domain, privacy: .public) code=\(error.code)")
        } else {
            logger.error("\(message)")
        }
    }

    private static func operationID(prefix: String) -> String {
        "\(prefix)-\(UUID().uuidString.lowercased())"
    }

    private static func providerSnapshot(for core: Np2mobileClientCore) -> Data {
        guard let raw = core.snapshotJSON().data(using: .utf8),
              var object = try? JSONSerialization.jsonObject(with: raw) as? [String: Any] else {
            return Data(#"{"state":"failed","carrier":"none"}"#.utf8)
        }
        object["version"] = Np2mobileVersion()
        object["server_routes"] = core.serverAddresses()
        object["data_plane"] = "direct-np2"
        object["cell_encryption"] = "chacha20-poly1305"
        if (object["cover_mode"] as? String)?.isEmpty != false {
            object["cover_mode"] = "unknown"
        }
        object["quic_fallbacks"] = 0
        object["carrier_pool_scale_ups"] = 0
        object["carrier_pool_failures"] = 0
        return (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
            ?? Data(#"{"state":"failed","carrier":"none"}"#.utf8)
    }

    private static func networkPathSignature(_ path: Network.NWPath) -> String {
        let types: [Network.NWInterface.InterfaceType] = [.wifi, .cellular, .wiredEthernet, .loopback, .other]
        let active = types.filter { path.usesInterfaceType($0) }
            .map { String(describing: $0) }
            .joined(separator: ",")
        return "\(path.status)-\(active)-v4:\(path.supportsIPv4)-v6:\(path.supportsIPv6)-exp:\(path.isExpensive)-con:\(path.isConstrained)"
    }

    private static func networkSettings(excluding serverRoutes: ServerRouteExclusions) -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "198.18.0.2")
        let ipv4 = NEIPv4Settings(addresses: ["198.18.0.1"], subnetMasks: ["255.255.255.252"])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        ipv4.excludedRoutes = serverRoutes.ipv4.map {
            NEIPv4Route(destinationAddress: $0, subnetMask: "255.255.255.255")
        }
        settings.ipv4Settings = ipv4

        let ipv6 = NEIPv6Settings(addresses: ["fd00:6e70:3200::1"], networkPrefixLengths: [64])
        ipv6.includedRoutes = [NEIPv6Route.default()]
        ipv6.excludedRoutes = serverRoutes.ipv6.map {
            NEIPv6Route(destinationAddress: $0, networkPrefixLength: 128)
        }
        settings.ipv6Settings = ipv6

        let dns = NEDNSSettings(servers: ["1.1.1.1", "1.0.0.1"])
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        settings.mtu = 1_500
        return settings
    }

    private static func xpcSafeError(_ error: Error) -> NSError {
        let source = error as NSError
        return NSError(
            domain: "com.neproto.ios.PacketTunnel",
            code: source.code,
            userInfo: [NSLocalizedDescriptionKey: source.localizedDescription]
        )
    }
}
private final class SendableClientCoreReference: @unchecked Sendable {
    let value: Np2mobileClientCore

    init(_ value: Np2mobileClientCore) {
        self.value = value
    }
}

private final class TunnelStartCompletion: @unchecked Sendable {
    private let lock = NSLock()
    private var callback: ((Error?) -> Void)?

    init(_ callback: @escaping (Error?) -> Void) { self.callback = callback }

    func call(_ error: Error?) {
        let owned = lock.withLock {
            defer { callback = nil }
            return callback
        }
        owned?(error)
    }
}

private final class TunnelStopCompletion: @unchecked Sendable {
    private let lock = NSLock()
    private var callback: (() -> Void)?

    init(_ callback: @escaping () -> Void) { self.callback = callback }

    func call() {
        let owned = lock.withLock {
            defer { callback = nil }
            return callback
        }
        owned?()
    }
}

private enum TunnelProviderError: Error, LocalizedError {
    case invalidConfiguration
    case alreadyActive
    case missingSecretReference
    case clientCoreUnavailable
    case invalidClientRoutes
    case connectFailed
    case missingTunnelFileDescriptor
    case packetDataPlaneFailed
    case runtimeStopped
    case reconnectFailed
    case stopping
    case providerReleased

    var errorDescription: String? {
        switch self {
        case .invalidConfiguration: "Некорректная конфигурация NP/2."
		case .alreadyActive: "Packet Tunnel уже запускается или подключён."
        case .missingSecretReference: "Ссылка на ключ NP/2 отсутствует."
        case .clientCoreUnavailable: "HTTPS ClientCore недоступен."
        case .invalidClientRoutes: "Некорректный снимок маршрутов NP/2."
        case .connectFailed: "HTTPS WebSocket соединение не установлено."
        case .missingTunnelFileDescriptor: "iOS не предоставила дескриптор utun."
        case .packetDataPlaneFailed: "Пакетный путь NP/2 не запустился."
        case .runtimeStopped: "Сессия NP/2 неожиданно остановилась."
        case .reconnectFailed: "HTTPS переподключение исчерпано."
        case .stopping: "Packet Tunnel уже останавливается."
        case .providerReleased: "Системный VPN provider был остановлен."
        }
    }
}
