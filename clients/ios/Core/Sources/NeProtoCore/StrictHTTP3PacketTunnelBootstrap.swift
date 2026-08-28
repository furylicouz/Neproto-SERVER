import Foundation

public enum StrictPacketTunnelBootstrapError: Error, Equatable {
    case invalidProviderConfiguration
    case invalidProfileID
    case invalidDeviceID
    case invalidCredentialReference
    case invalidProfilePayload
    case invalidClientConfiguration
    case invalidClientRoutes
    case alternateCarrierConfigured
}

public typealias StrictHTTP3PacketTunnelBootstrapError = StrictPacketTunnelBootstrapError
public typealias StrictHTTPSPacketTunnelBootstrapError = StrictPacketTunnelBootstrapError

private enum StrictPacketTunnelCarrier {
    case http3
    case https
}

private struct StrictPacketTunnelSnapshot: Sendable {
    let profile: ServerProfile
    let profileID: String
    let clientConfigurationJSON: String
    let clientRoutesJSON: String
    let credentialReference: Data
}

private enum StrictPacketTunnelLimits {
    static let maximumProfilePayloadBytes = 256 * 1024
    static let maximumClientConfigurationBytes = 64 * 1024
    static let maximumClientRoutesBytes = 256 * 1024
    static let maximumCredentialReferenceBytes = 16 * 1024
}

/// Bounded immutable input for the native strict HTTP/3 Packet Tunnel core.
/// Credential values remain in Keychain and are never stored in this snapshot.
public struct StrictHTTP3PacketTunnelBootstrap: Sendable {
    public static let maximumProfilePayloadBytes = StrictPacketTunnelLimits.maximumProfilePayloadBytes
    public static let maximumClientConfigurationBytes = StrictPacketTunnelLimits.maximumClientConfigurationBytes
    public static let maximumClientRoutesBytes = StrictPacketTunnelLimits.maximumClientRoutesBytes
    public static let maximumCredentialReferenceBytes = StrictPacketTunnelLimits.maximumCredentialReferenceBytes

    public let profile: ServerProfile
    public let profileID: String
    public let clientConfigurationJSON: String
    public let clientRoutesJSON: String
    public let credentialReference: Data

    public init(
        providerConfiguration: [String: Any],
        credentialReference: Data
    ) throws {
        let snapshot = try makeStrictPacketTunnelSnapshot(
            providerConfiguration: providerConfiguration,
            credentialReference: credentialReference,
            carrier: .http3
        )
        profile = snapshot.profile
        profileID = snapshot.profileID
        clientConfigurationJSON = snapshot.clientConfigurationJSON
        clientRoutesJSON = snapshot.clientRoutesJSON
        self.credentialReference = snapshot.credentialReference
    }
}

/// Bounded immutable input for the native strict HTTPS WebSocket A/B core.
/// It deliberately excludes HTTP/3, WebRTC, datagrams, cover traffic, and pools.
public struct StrictHTTPSPacketTunnelBootstrap: Sendable {
    public static let maximumProfilePayloadBytes = StrictPacketTunnelLimits.maximumProfilePayloadBytes
    public static let maximumClientConfigurationBytes = StrictPacketTunnelLimits.maximumClientConfigurationBytes
    public static let maximumClientRoutesBytes = StrictPacketTunnelLimits.maximumClientRoutesBytes
    public static let maximumCredentialReferenceBytes = StrictPacketTunnelLimits.maximumCredentialReferenceBytes

    public let profile: ServerProfile
    public let profileID: String
    public let clientConfigurationJSON: String
    public let clientRoutesJSON: String
    public let credentialReference: Data

    public init(
        providerConfiguration: [String: Any],
        credentialReference: Data
    ) throws {
        let snapshot = try makeStrictPacketTunnelSnapshot(
            providerConfiguration: providerConfiguration,
            credentialReference: credentialReference,
            carrier: .https
        )
        profile = snapshot.profile
        profileID = snapshot.profileID
        clientConfigurationJSON = snapshot.clientConfigurationJSON
        clientRoutesJSON = snapshot.clientRoutesJSON
        self.credentialReference = snapshot.credentialReference
    }
}

private func makeStrictPacketTunnelSnapshot(
    providerConfiguration: [String: Any],
    credentialReference: Data,
    carrier: StrictPacketTunnelCarrier
) throws -> StrictPacketTunnelSnapshot {
    guard !credentialReference.isEmpty,
          credentialReference.count <= StrictPacketTunnelLimits.maximumCredentialReferenceBytes else {
        throw StrictPacketTunnelBootstrapError.invalidCredentialReference
    }
    guard let rawProfileID = providerConfiguration["profile_id"] as? String,
          let profileID = UUID(uuidString: rawProfileID),
          profileID.uuidString.lowercased() == rawProfileID else {
        throw StrictPacketTunnelBootstrapError.invalidProfileID
    }
    guard let rawDeviceID = providerConfiguration["device_id"] as? String,
          let deviceID = UUID(uuidString: rawDeviceID),
          deviceID.uuidString.lowercased() == rawDeviceID else {
        throw StrictPacketTunnelBootstrapError.invalidDeviceID
    }
    guard let profilePayload = providerConfiguration["profile_payload"] as? Data,
          !profilePayload.isEmpty,
          profilePayload.count <= StrictPacketTunnelLimits.maximumProfilePayloadBytes else {
        throw StrictPacketTunnelBootstrapError.invalidProfilePayload
    }
    let profile: ServerProfile
    do {
        profile = try ServerProfile(providerPayload: profilePayload)
    } catch {
        throw StrictPacketTunnelBootstrapError.invalidProfilePayload
    }
    guard profile.id == profileID else {
        throw StrictPacketTunnelBootstrapError.invalidProviderConfiguration
    }
    guard let routesData = providerConfiguration["client_routes"] as? Data,
          !routesData.isEmpty,
          routesData.count <= StrictPacketTunnelLimits.maximumClientRoutesBytes,
          let routesJSON = String(data: routesData, encoding: .utf8),
          (try? JSONSerialization.jsonObject(with: routesData)) is [Any] else {
        throw StrictPacketTunnelBootstrapError.invalidClientRoutes
    }

    let clientData: Data
    do {
        switch carrier {
        case .http3:
            clientData = try profile.strictHTTP3ClientConfigurationJSON(deviceID: deviceID)
        case .https:
            clientData = try profile.strictHTTPSClientConfigurationJSON(deviceID: deviceID)
        }
    } catch {
        throw StrictPacketTunnelBootstrapError.invalidClientConfiguration
    }
    guard !clientData.isEmpty,
          clientData.count <= StrictPacketTunnelLimits.maximumClientConfigurationBytes,
          let clientJSON = String(data: clientData, encoding: .utf8),
          let clientObject = try? JSONSerialization.jsonObject(with: clientData) as? [String: Any],
          clientObject["max_parallel_carriers"] as? Int == 1,
          clientObject["cover_mode"] as? String == "off" else {
        throw StrictPacketTunnelBootstrapError.invalidClientConfiguration
    }
    try validateStrictCarrier(clientObject, carrier: carrier)

    return StrictPacketTunnelSnapshot(
        profile: profile,
        profileID: rawProfileID,
        clientConfigurationJSON: clientJSON,
        clientRoutesJSON: routesJSON,
        credentialReference: credentialReference
    )
}

private func validateStrictCarrier(
    _ clientObject: [String: Any],
    carrier: StrictPacketTunnelCarrier
) throws {
    switch carrier {
    case .http3:
        guard clientObject["carrier_policy"] as? String == "http3-only",
              let rawHTTP3URL = clientObject["http3_url"] as? String,
              let http3URL = URL(string: rawHTTP3URL),
              http3URL.scheme == "https",
              http3URL.host != nil,
              clientObject["http3_timeout"] is String else {
            throw StrictPacketTunnelBootstrapError.invalidClientConfiguration
        }
        for field in ["https_url", "https_timeout", "webrtc_signaling_url", "webrtc_timeout"]
        where clientObject[field] != nil {
            throw StrictPacketTunnelBootstrapError.alternateCarrierConfigured
        }
    case .https:
        guard clientObject["carrier_policy"] as? String == "https-only",
              clientObject["require_datagrams"] as? Bool == false,
              let rawHTTPSURL = clientObject["https_url"] as? String,
              let httpsURL = URL(string: rawHTTPSURL),
              httpsURL.scheme == "wss",
              httpsURL.host != nil,
              clientObject["https_timeout"] is String else {
            throw StrictPacketTunnelBootstrapError.invalidClientConfiguration
        }
        for field in ["http3_url", "http3_timeout", "webrtc_signaling_url", "webrtc_timeout"]
        where clientObject[field] != nil {
            throw StrictPacketTunnelBootstrapError.alternateCarrierConfigured
        }
    }
}
