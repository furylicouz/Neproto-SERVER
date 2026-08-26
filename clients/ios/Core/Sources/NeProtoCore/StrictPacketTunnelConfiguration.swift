import Foundation

public enum StrictPacketTunnelConfigurationError: Error, Equatable {
    case invalidProviderConfiguration
    case invalidProfileID
    case invalidCredentialReference
    case invalidClientConfiguration
    case invalidClientRoutes
    case alternateCarrierConfigured
}

/// A bounded, immutable bootstrap snapshot consumed by the Packet Tunnel.
/// It accepts no credential value and rejects every alternate carrier field.
public struct StrictPacketTunnelConfiguration: Sendable {
    public static let maximumClientConfigurationBytes = 64 * 1024
    public static let maximumClientRoutesBytes = 256 * 1024
    public static let maximumCredentialReferenceBytes = 16 * 1024

    public let profileID: String
    public let clientConfigurationJSON: String
    public let clientRoutesJSON: String
    public let credentialReference: Data

    public init(
        providerConfiguration: [String: Any],
        credentialReference: Data
    ) throws {
        guard providerConfiguration["carrier_policy"] as? String == "http3-only" else {
            throw StrictPacketTunnelConfigurationError.alternateCarrierConfigured
        }
        guard let profileID = providerConfiguration["profile_id"] as? String,
              let parsedProfileID = UUID(uuidString: profileID),
              parsedProfileID.uuidString.lowercased() == profileID else {
            throw StrictPacketTunnelConfigurationError.invalidProfileID
        }
        guard !credentialReference.isEmpty,
              credentialReference.count <= Self.maximumCredentialReferenceBytes else {
            throw StrictPacketTunnelConfigurationError.invalidCredentialReference
        }
        guard let clientData = providerConfiguration["client_configuration"] as? Data,
              !clientData.isEmpty,
              clientData.count <= Self.maximumClientConfigurationBytes,
              let clientJSON = String(data: clientData, encoding: .utf8),
              let clientObject = try? JSONSerialization.jsonObject(with: clientData) as? [String: Any] else {
            throw StrictPacketTunnelConfigurationError.invalidClientConfiguration
        }
        guard clientObject["carrier_policy"] as? String == "http3-only",
              clientObject["max_parallel_carriers"] as? Int == 1,
              let rawHTTP3URL = clientObject["http3_url"] as? String,
              let http3URL = URL(string: rawHTTP3URL),
              http3URL.scheme == "https",
              http3URL.host != nil else {
            throw StrictPacketTunnelConfigurationError.invalidClientConfiguration
        }
        for field in ["https_url", "https_timeout", "webrtc_signaling_url", "webrtc_timeout"]
        where clientObject[field] != nil {
            throw StrictPacketTunnelConfigurationError.alternateCarrierConfigured
        }
        guard let routesData = providerConfiguration["client_routes"] as? Data,
              !routesData.isEmpty,
              routesData.count <= Self.maximumClientRoutesBytes,
              let routesJSON = String(data: routesData, encoding: .utf8),
              (try? JSONSerialization.jsonObject(with: routesData)) is [Any] else {
            throw StrictPacketTunnelConfigurationError.invalidClientRoutes
        }

        self.profileID = profileID
        self.clientConfigurationJSON = clientJSON
        self.clientRoutesJSON = routesJSON
        self.credentialReference = credentialReference
    }
}
