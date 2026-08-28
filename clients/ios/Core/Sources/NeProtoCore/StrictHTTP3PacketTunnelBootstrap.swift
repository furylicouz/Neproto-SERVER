import Foundation

public enum StrictHTTP3PacketTunnelBootstrapError: Error, Equatable {
    case invalidProviderConfiguration
    case invalidProfileID
    case invalidDeviceID
    case invalidCredentialReference
    case invalidProfilePayload
    case invalidClientConfiguration
    case invalidClientRoutes
    case alternateCarrierConfigured
}

/// Bounded immutable input for the native strict HTTP/3 Packet Tunnel core.
/// Credential values remain in Keychain and are never stored in this snapshot.
public struct StrictHTTP3PacketTunnelBootstrap: Sendable {
    public static let maximumProfilePayloadBytes = 256 * 1024
    public static let maximumClientConfigurationBytes = 64 * 1024
    public static let maximumClientRoutesBytes = 256 * 1024
    public static let maximumCredentialReferenceBytes = 16 * 1024

    public let profile: ServerProfile
    public let profileID: String
    public let clientConfigurationJSON: String
    public let clientRoutesJSON: String
    public let credentialReference: Data

    public init(
        providerConfiguration: [String: Any],
        credentialReference: Data
    ) throws {
        guard !credentialReference.isEmpty,
              credentialReference.count <= Self.maximumCredentialReferenceBytes else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidCredentialReference
        }
        guard let rawProfileID = providerConfiguration["profile_id"] as? String,
              let profileID = UUID(uuidString: rawProfileID),
              profileID.uuidString.lowercased() == rawProfileID else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidProfileID
        }
        guard let rawDeviceID = providerConfiguration["device_id"] as? String,
              let deviceID = UUID(uuidString: rawDeviceID),
              deviceID.uuidString.lowercased() == rawDeviceID else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidDeviceID
        }
        guard let profilePayload = providerConfiguration["profile_payload"] as? Data,
              !profilePayload.isEmpty,
              profilePayload.count <= Self.maximumProfilePayloadBytes else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidProfilePayload
        }
        let profile: ServerProfile
        do {
            profile = try ServerProfile(providerPayload: profilePayload)
        } catch {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidProfilePayload
        }
        guard profile.id == profileID else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidProviderConfiguration
        }
        guard let routesData = providerConfiguration["client_routes"] as? Data,
              !routesData.isEmpty,
              routesData.count <= Self.maximumClientRoutesBytes,
              let routesJSON = String(data: routesData, encoding: .utf8),
              (try? JSONSerialization.jsonObject(with: routesData)) is [Any] else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidClientRoutes
        }

        let clientData: Data
        do {
            clientData = try profile.strictHTTP3ClientConfigurationJSON(deviceID: deviceID)
        } catch {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidClientConfiguration
        }
        guard !clientData.isEmpty,
              clientData.count <= Self.maximumClientConfigurationBytes,
              let clientJSON = String(data: clientData, encoding: .utf8),
              let clientObject = try? JSONSerialization.jsonObject(with: clientData) as? [String: Any],
              clientObject["carrier_policy"] as? String == "http3-only",
              clientObject["max_parallel_carriers"] as? Int == 1,
              clientObject["cover_mode"] as? String == "off",
              let rawHTTP3URL = clientObject["http3_url"] as? String,
              let http3URL = URL(string: rawHTTP3URL),
              http3URL.scheme == "https",
              http3URL.host != nil,
              clientObject["http3_timeout"] is String else {
            throw StrictHTTP3PacketTunnelBootstrapError.invalidClientConfiguration
        }
        for field in ["https_url", "https_timeout", "webrtc_signaling_url", "webrtc_timeout"]
        where clientObject[field] != nil {
            throw StrictHTTP3PacketTunnelBootstrapError.alternateCarrierConfigured
        }

        self.profile = profile
        self.profileID = rawProfileID
        self.clientConfigurationJSON = clientJSON
        self.clientRoutesJSON = routesJSON
        self.credentialReference = credentialReference
    }
}
