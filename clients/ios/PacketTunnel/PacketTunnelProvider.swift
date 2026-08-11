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
    private let logger = Logger(subsystem: "NeProto", category: "PacketTunnel")
    private var monitor: DispatchSourceTimer?
    private var pathMonitor: NWPathMonitor?
    private var lastNetworkPathSignature: String?
    private var stopping = false

    override func startTunnel(
        options: [String: NSObject]? = nil,
        completionHandler: @escaping (Error?) -> Void
    ) {
        logger.notice("Packet Tunnel start requested")
        let completion = TunnelStartCompletion(completionHandler)
        stateLock.lock()
        stopping = false
        stateLock.unlock()
        do {
            let tunnelProtocol = try requireTunnelProtocol()
            let profile = try requireProfile(from: tunnelProtocol)
            guard let persistentReference = tunnelProtocol.passwordReference else {
                throw TunnelProviderError.missingSecretReference
            }
            let secret = try KeychainSecretStore().read(persistentReference: persistentReference)
            try profile.validate(secret: secret)
            logger.notice("Profile and Keychain secret validated")
            guard let rawDeviceID = tunnelProtocol.providerConfiguration?["device_id"] as? String,
                  let deviceID = UUID(uuidString: rawDeviceID),
                  deviceID != UUID(uuidString: "00000000-0000-0000-0000-000000000000") else {
                throw TunnelProviderError.invalidConfiguration
            }
            let clientJSON = String(
                decoding: try profile.clientConfigurationJSON(deviceID: deviceID),
                as: UTF8.self
            )

            let routeData = tunnelProtocol.providerConfiguration?["client_routes"] as? Data ?? Data("[]".utf8)
            guard let routeJSON = String(data: routeData, encoding: .utf8) else {
                throw TunnelProviderError.invalidClientRoutes
            }
            var routeError: NSError?
            guard Np2mobileSetClientRoutesJSON(routeJSON, &routeError) else {
                logMobileFailure("NP/2 client route snapshot rejected", error: routeError)
                throw routeError ?? TunnelProviderError.invalidClientRoutes
            }
            logger.notice("Immutable NP/2 client route snapshot installed")

            var mobileError: NSError?
            guard Np2mobileStart(clientJSON, secret, &mobileError) else {
                logMobileFailure("NP/2 encrypted session start failed", error: mobileError)
                throw mobileError ?? TunnelProviderError.np2StartFailed
            }
            let routeExclusions = try ServerRouteExclusions(Np2mobileServerAddresses())
            let excludedIPv4 = routeExclusions.ipv4.joined(separator: ",")
            let excludedIPv6 = routeExclusions.ipv6.joined(separator: ",")
            logger.notice(
                "Carrier route exclusions: IPv4=\(excludedIPv4, privacy: .public) IPv6=\(excludedIPv6, privacy: .public)"
            )
            logger.notice("Encrypted NP/2 session connected")

            setTunnelNetworkSettings(Self.networkSettings(excluding: routeExclusions)) { [weak self] error in
                guard let self else {
                    Np2mobileStop()
                    completion.call(TunnelProviderError.providerReleased)
                    return
                }
                if let error {
                    self.logger.error("Installing tunnel network settings failed")
                    Np2mobileStop()
                    completion.call(Self.xpcSafeError(error))
                    return
                }
                let descriptor = neproto_duplicate_tunnel_file_descriptor()
                guard descriptor >= 0 else {
                    self.logger.error("No duplicated utun file descriptor was found")
                    Np2mobileStop()
                    completion.call(Self.xpcSafeError(TunnelProviderError.missingTunnelFileDescriptor))
                    return
                }
                var tunnelError: NSError?
                guard Np2mobileStartPacketTunnel(Int64(descriptor), &tunnelError) else {
                    self.logMobileFailure("Direct NP/2 packet data plane failed", error: tunnelError)
                    Np2mobileStop()
                    completion.call(Self.xpcSafeError(tunnelError ?? TunnelProviderError.packetDataPlaneFailed))
                    return
                }
                self.logger.notice("Direct utun-to-NP/2 data plane is running")
                self.startRuntimeMonitor()
                self.startNetworkPathMonitor()
                completion.call(nil)
            }
        } catch {
            let nsError = error as NSError
            logger.error("Packet Tunnel start failed: domain=\(nsError.domain, privacy: .public) code=\(nsError.code)")
            Np2mobileStop()
            completionHandler(Self.xpcSafeError(error))
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        logger.notice("Packet Tunnel stop requested, reason=\(reason.rawValue)")
        let completion = TunnelStopCompletion(completionHandler)
        stateLock.lock()
        stopping = true
        let activeMonitor = monitor
        monitor = nil
        let activePathMonitor = pathMonitor
        pathMonitor = nil
        lastNetworkPathSignature = nil
        stateLock.unlock()
        activeMonitor?.cancel()
        activePathMonitor?.cancel()
        runtimeQueue.async {
            let startedAt = DispatchTime.now().uptimeNanoseconds
            Np2mobileStop()
            let elapsedMilliseconds = (DispatchTime.now().uptimeNanoseconds - startedAt) / 1_000_000
            self.logger.notice("NP/2 runtime stopped in \(elapsedMilliseconds, privacy: .public) ms")
            completion.call()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        if String(data: messageData, encoding: .utf8) == "np2-cluster-catalog" {
            var catalogError: NSError?
            let catalog = Np2mobileCatalogJSON(&catalogError)
            if let catalogError {
                logger.error("Cluster catalog request failed: domain=\(catalogError.domain, privacy: .public) code=\(catalogError.code)")
                completionHandler?(nil)
                return
            }
            completionHandler?(catalog.data(using: .utf8))
            return
        }
        let response: [String: Any] = [
            "state": Np2mobileState(),
            "version": Np2mobileVersion(),
            "last_error": Np2mobileLastError(),
            "server_routes": Np2mobileServerAddresses(),
            "carrier": Np2mobileCarrier(),
            "data_plane": "direct-np2",
            "cell_encryption": "chacha20-poly1305",
            "upload_bytes_per_second": Np2mobileUploadBytesPerSecond(),
            "download_bytes_per_second": Np2mobileDownloadBytesPerSecond(),
            "upload_total_bytes": Np2mobileUploadTotalBytes(),
            "download_total_bytes": Np2mobileDownloadTotalBytes(),
            "udp_mode": Np2mobileUDPMode(),
            "quic_fallbacks": Np2mobileQUICFallbackCount(),
            "carrier_pool_target": Np2mobileCarrierPoolTarget(),
            "carrier_pool_healthy": Np2mobileCarrierPoolHealthy(),
            "carrier_pool_assignments": Np2mobileCarrierPoolAssignments(),
            "carrier_pool_scale_ups": Np2mobileCarrierPoolScaleUpCount(),
            "carrier_pool_failures": Np2mobileCarrierPoolFailureCount(),
            "dns_attribution_queries": Np2mobileDNSAttributionQueryCount(),
            "dns_attribution_responses": Np2mobileDNSAttributionResponseCount(),
            "dns_attribution_hits": Np2mobileDNSAttributionHitCount(),
            "dns_attribution_misses": Np2mobileDNSAttributionMissCount(),
            "dns_attribution_cached": Np2mobileDNSAttributionCachedCount(),
            "first_flight_domain_hits": Np2mobileFirstFlightDomainHitCount(),
            "first_flight_fallbacks": Np2mobileFirstFlightFallbackCount(),
            "tcp_stream_attempts": Np2mobileTCPStreamAttemptCount(),
            "tcp_stream_successes": Np2mobileTCPStreamSuccessCount(),
            "tcp_stream_failures": Np2mobileTCPStreamFailureCount(),
            "active_streams": Np2mobileActiveStreamCount(),
            "flow_control_stalls": Np2mobileFlowControlStallCount(),
            "protocol_errors": Np2mobileProtocolErrorCount(),
            "sent_cells": Np2mobileSentCellCount(),
            "received_cells": Np2mobileReceivedCellCount(),
            "sent_cell_payload_bytes": Np2mobileSentCellPayloadByteCount(),
            "received_payload_bytes": Np2mobileReceivedPayloadByteCount(),
            "window_updates_sent": Np2mobileWindowUpdateSentCount(),
            "window_updates_received": Np2mobileWindowUpdateReceivedCount(),
            "cover_real_wire_bytes": Np2mobileCoverRealWireByteCount(),
            "cover_padding_bytes": Np2mobileCoverPaddingByteCount(),
            "cover_dummy_wire_bytes": Np2mobileCoverDummyWireByteCount(),
            "cover_profile_transitions": Np2mobileCoverProfileTransitionCount(),
            "cover_web_sessions": Np2mobileCoverWebSessionCount(),
            "cover_realtime_sessions": Np2mobileCoverRealtimeSessionCount(),
            "cover_stream_sessions": Np2mobileCoverStreamSessionCount(),
            "network_changes": Np2mobileNetworkChangeCount(),
            "reconnects": Np2mobileReconnectCount(),
            "migrations": Np2mobileMigrationCount(),
        ]
        completionHandler?(try? JSONSerialization.data(withJSONObject: response, options: [.sortedKeys]))
    }

    private func requireTunnelProtocol() throws -> NETunnelProviderProtocol {
        guard let tunnelProtocol = protocolConfiguration as? NETunnelProviderProtocol else {
            throw TunnelProviderError.invalidConfiguration
        }
        return tunnelProtocol
    }

    private func requireProfile(from tunnelProtocol: NETunnelProviderProtocol) throws -> ServerProfile {
        guard let payload = tunnelProtocol.providerConfiguration?["profile_payload"] as? Data else {
            throw TunnelProviderError.invalidConfiguration
        }
        return try ServerProfile(providerPayload: payload)
    }

    private func startRuntimeMonitor() {
        let timer = DispatchSource.makeTimerSource(queue: runtimeQueue)
        timer.schedule(deadline: .now() + 1, repeating: 1)
        timer.setEventHandler { [weak self] in
            guard let self else { return }
            let state = Np2mobileState()
            guard state == "failed" || state == "stopped" else { return }
            self.stateLock.lock()
            let shouldCancel = !self.stopping
            self.stopping = true
            let activeMonitor = self.monitor
            self.monitor = nil
            self.stateLock.unlock()
            activeMonitor?.cancel()
            guard shouldCancel else { return }
            let detail = Np2mobileLastError()
            self.logger.error("NP/2 runtime stopped unexpectedly: \(detail, privacy: .public)")
            self.cancelTunnelWithError(
                NSError(
                    domain: "com.neproto.ios.PacketTunnel",
                    code: 1,
                    userInfo: [NSLocalizedDescriptionKey: "NP/2 runtime stopped: \(detail)"]
                )
            )
        }
        stateLock.lock()
        monitor?.cancel()
        monitor = timer
        stateLock.unlock()
        timer.resume()
    }

    private func startNetworkPathMonitor() {
        let pathMonitor = NWPathMonitor()
        pathMonitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let signature = Self.networkPathSignature(path)
            self.stateLock.lock()
            let changed = self.lastNetworkPathSignature.map { $0 != signature } ?? false
            self.lastNetworkPathSignature = signature
            self.stateLock.unlock()
            guard changed else { return }
            self.logger.notice("Network path changed; probing active NP/2 carrier")
            self.migrationQueue.async { [weak self] in
                guard let self else { return }
                self.stateLock.lock()
                let shouldMigrate = !self.stopping
                self.stateLock.unlock()
                guard shouldMigrate else { return }
                var migrationError: NSError?
                guard Np2mobileNetworkChanged(&migrationError) else {
                    self.logMobileFailure("NP/2 carrier migration did not complete", error: migrationError)
                    return
                }
                self.logger.notice("NP/2 network transition completed: carrier=\(Np2mobileCarrier(), privacy: .public) reconnects=\(Np2mobileReconnectCount(), privacy: .public)")
            }
        }
        stateLock.lock()
        self.pathMonitor?.cancel()
        self.pathMonitor = pathMonitor
        lastNetworkPathSignature = nil
        stateLock.unlock()
        pathMonitor.start(queue: runtimeQueue)
    }

    private static func networkPathSignature(_ path: NWPath) -> String {
        let interfaceTypes: [NWInterface.InterfaceType] = [.wifi, .cellular, .wiredEthernet, .loopback, .other]
        let activeTypes = interfaceTypes.filter { path.usesInterfaceType($0) }
            .map { String(describing: $0) }
            .joined(separator: ",")
        return "\(path.status)-\(activeTypes)-v4:\(path.supportsIPv4)-v6:\(path.supportsIPv6)-exp:\(path.isExpensive)-con:\(path.isConstrained)"
    }

    private func logMobileFailure(_ message: StaticString, error: NSError?) {
        if let error {
            logger.error("\(message): domain=\(error.domain, privacy: .public) code=\(error.code)")
        } else {
            logger.error("\(message)")
        }
    }

    private static func networkSettings(
        excluding serverRoutes: ServerRouteExclusions
    ) -> NEPacketTunnelNetworkSettings {
        // The NP/2 carrier is authenticated before this default route is
        // installed. A carrier failure terminates the tunnel instead of
        // reconnecting through its own utun route.
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

        // DNS packets travel inside the encrypted NP/2 tunnel. Keeping DNS on
        // the tunnel data plane lets NP2Mobile retain a short-lived in-memory
        // domain-to-address attribution for authoritative domain/GeoSite routes.
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
    private let callback: (Error?) -> Void

    init(_ callback: @escaping (Error?) -> Void) {
        self.callback = callback
    }

    func call(_ error: Error?) {
        callback(error)
    }
}

private final class TunnelStopCompletion: @unchecked Sendable {
    private let callback: () -> Void

    init(_ callback: @escaping () -> Void) {
        self.callback = callback
    }

    func call() {
        callback()
    }
}

private enum TunnelProviderError: Error, LocalizedError {
    case invalidConfiguration
    case missingSecretReference
    case np2StartFailed
    case missingTunnelFileDescriptor
    case packetDataPlaneFailed
    case runtimeStopped
    case providerReleased
    case invalidClientRoutes

    var errorDescription: String? {
        switch self {
        case .invalidConfiguration: "Некорректная конфигурация NP/2."
        case .missingSecretReference: "Ссылка на ключ NP/2 отсутствует."
        case .np2StartFailed: "Зашифрованная сессия NP/2 не запустилась."
        case .missingTunnelFileDescriptor: "iOS не предоставила дескриптор utun."
        case .packetDataPlaneFailed: "Прямой сетевой стек NP/2 не запустился."
        case .runtimeStopped: "Сессия NP/2 неожиданно остановилась."
        case .providerReleased: "Системный VPN provider был остановлен."
        case .invalidClientRoutes: "Некорректный снимок локальных маршрутов NP/2."
        }
    }
}
