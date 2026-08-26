import Foundation
import NeProtoCore
import Network
@preconcurrency import NetworkExtension
import NP2Mobile
import OSLog

final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    private let runtimeQueue = DispatchQueue(label: "com.neproto.packet-tunnel.runtime", qos: .userInitiated)
    private let migrationQueue = DispatchQueue(label: "com.neproto.packet-tunnel.migration", qos: .utility)
    private let stateLock = NSLock()
    private let logger = Logger(subsystem: "NeProto", category: "StrictPacketTunnel")

    private var core: Np2mobileClientCore?
    private var runtimeMonitor: DispatchSourceTimer?
    private var pathMonitor: NWPathMonitor?
    private var lastPathSignature: String?
    private var stopping = false

    override func startTunnel(
        options _: [String: NSObject]? = nil,
        completionHandler: @escaping (Error?) -> Void
    ) {
        let completion = TunnelStartCompletion(completionHandler)
        stateLock.withLock { stopping = false }
        runtimeQueue.async { [weak self] in
            self?.startStrictHTTP3Tunnel(completion: completion)
                ?? completion.call(Self.xpcSafeError(TunnelProviderError.providerReleased))
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        logger.notice("Stopping strict HTTP/3 Packet Tunnel, reason=\(reason.rawValue)")
        let completion = TunnelStopCompletion(completionHandler)
        let owned: Np2mobileClientCore? = stateLock.withLock {
            stopping = true
            runtimeMonitor?.cancel()
            runtimeMonitor = nil
            pathMonitor?.cancel()
            pathMonitor = nil
            lastPathSignature = nil
            let result = core
            core = nil
            return result
        }
        runtimeQueue.async {
            var closeError: NSError?
            if let owned, !owned.close(&closeError) {
                self.logMobileFailure("Closing strict HTTP/3 ClientCore failed", error: closeError)
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
        completionHandler?(Data(activeCore.snapshotJSON().utf8))
    }

    private func startStrictHTTP3Tunnel(completion: TunnelStartCompletion) {
        var createdCore: Np2mobileClientCore?
        do {
            let tunnelProtocol = try requireTunnelProtocol()
            guard let persistentReference = tunnelProtocol.passwordReference else {
                throw TunnelProviderError.missingSecretReference
            }
            let bootstrap = try StrictPacketTunnelConfiguration(
                providerConfiguration: tunnelProtocol.providerConfiguration ?? [:],
                credentialReference: persistentReference
            )
            let secret = try KeychainSecretStore().read(persistentReference: bootstrap.credentialReference)

            var mobileError: NSError?
            guard let newCore = Np2mobileNewStrictHTTP3ClientCore(&mobileError) else {
                throw mobileError ?? TunnelProviderError.clientCoreUnavailable
            }
            createdCore = newCore
            guard stateLock.withLock({
                guard !stopping, core == nil else { return false }
                core = newCore
                return true
            }) else {
                throw TunnelProviderError.stopping
            }
            guard newCore.setClientRoutesJSON(bootstrap.clientRoutesJSON, error: &mobileError) else {
                throw mobileError ?? TunnelProviderError.invalidClientRoutes
            }
            guard newCore.connect(
                bootstrap.clientConfigurationJSON,
                secret: secret,
                operationID: Self.operationID(prefix: "ios-start"),
                profileID: bootstrap.profileID,
                error: &mobileError
            ) else {
                throw mobileError ?? TunnelProviderError.connectFailed
            }
            let exclusions = try ServerRouteExclusions(newCore.serverAddresses())
            guard !stateLock.withLock({ stopping }) else {
                throw TunnelProviderError.stopping
            }

            setTunnelNetworkSettings(Self.networkSettings(excluding: exclusions)) { [weak self] settingsError in
                guard let self else {
                    var closeError: NSError?
                    _ = newCore.close(&closeError)
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
                        var attachError: NSError?
                        guard newCore.attachPacketTunnel(Int64(descriptor), mtu: 1_500, error: &attachError) else {
                            neproto_close_tunnel_file_descriptor(descriptor)
                            throw attachError ?? TunnelProviderError.packetDataPlaneFailed
                        }
                        self.startRuntimeMonitor(for: newCore)
                        self.startNetworkPathMonitor(for: newCore)
                        self.logger.notice("Strict HTTP/3 WebTransport Packet Tunnel is connected")
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
        if let failedCore {
            stateLock.withLock {
                if core === failedCore { core = nil }
            }
            var closeError: NSError?
            _ = failedCore.close(&closeError)
        }
        let safeError = Self.xpcSafeError(error)
        logger.error("Strict HTTP/3 Packet Tunnel start failed: domain=\(safeError.domain, privacy: .public) code=\(safeError.code)")
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
        stateLock.withLock {
            runtimeMonitor?.cancel()
            runtimeMonitor = timer
        }
        timer.resume()
    }

    private func startNetworkPathMonitor(for expectedCore: Np2mobileClientCore) {
        let monitor = NWPathMonitor()
        monitor.pathUpdateHandler = { [weak self, weak expectedCore] path in
            guard let self, let expectedCore else { return }
            let signature = Self.networkPathSignature(path)
            let changed = self.stateLock.withLock {
                guard self.core === expectedCore, !self.stopping else { return false }
                defer { self.lastPathSignature = signature }
                return self.lastPathSignature.map { $0 != signature } ?? false
            }
            guard changed else { return }
            self.migrationQueue.async { [weak self, weak expectedCore] in
                guard let self, let expectedCore,
                      self.stateLock.withLock({ self.core === expectedCore && !self.stopping }) else { return }
                self.reasserting = true
                var migrationError: NSError?
                let migrated = expectedCore.networkChanged(
                    Self.operationID(prefix: "ios-network"),
                    error: &migrationError
                )
                self.reasserting = false
                guard migrated else {
                    self.logMobileFailure("Strict HTTP/3 reconnect exhausted", error: migrationError)
                    self.terminateAfterRuntimeFailure(
                        expectedCore,
                        error: migrationError ?? TunnelProviderError.reconnectFailed
                    )
                    return
                }
                self.logger.notice("Network transition retained strict HTTP/3 WebTransport")
            }
        }
        stateLock.withLock {
            pathMonitor?.cancel()
            pathMonitor = monitor
            lastPathSignature = nil
        }
        monitor.start(queue: runtimeQueue)
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
            return true
        }
        guard shouldTerminate else { return }
        var closeError: NSError?
        _ = failedCore.close(&closeError)
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

    private static func networkPathSignature(_ path: NWPath) -> String {
        let types: [NWInterface.InterfaceType] = [.wifi, .cellular, .wiredEthernet, .loopback, .other]
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
        case .missingSecretReference: "Ссылка на ключ NP/2 отсутствует."
        case .clientCoreUnavailable: "HTTP/3 ClientCore недоступен."
        case .invalidClientRoutes: "Некорректный снимок маршрутов NP/2."
        case .connectFailed: "HTTP/3 WebTransport соединение не установлено."
        case .missingTunnelFileDescriptor: "iOS не предоставила дескриптор utun."
        case .packetDataPlaneFailed: "Пакетный путь NP/2 не запустился."
        case .runtimeStopped: "Сессия NP/2 неожиданно остановилась."
        case .reconnectFailed: "HTTP/3 переподключение исчерпано."
        case .stopping: "Packet Tunnel уже останавливается."
        case .providerReleased: "Системный VPN provider был остановлен."
        }
    }
}
