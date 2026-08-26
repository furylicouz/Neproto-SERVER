import Foundation
import Testing
@testable import NeProtoCore

@Suite("Strict Packet Tunnel bootstrap")
struct StrictPacketTunnelConfigurationTests {
    @Test("accepts one bounded HTTP/3-only snapshot without secret material")
    func acceptsStrictSnapshot() throws {
        let provider = try validProviderConfiguration()
        let reference = Data([0x01, 0x02, 0x03])

        let parsed = try StrictPacketTunnelConfiguration(
            providerConfiguration: provider,
            credentialReference: reference
        )

        #expect(parsed.profileID == "79d6ac07-a320-42d7-8f8f-1b8576ee7bd1")
        #expect(parsed.clientRoutesJSON == "[]")
        #expect(parsed.credentialReference == reference)
        #expect(!parsed.clientConfigurationJSON.lowercased().contains("secret"))
        #expect(!parsed.clientConfigurationJSON.contains("webrtc"))
        #expect(!parsed.clientConfigurationJSON.contains("https_url"))
    }

    @Test("rejects alternate carrier fields even under an HTTP/3-only label")
    func rejectsAlternateCarrierFields() throws {
        var provider = try validProviderConfiguration()
        var client = try #require(
            JSONSerialization.jsonObject(
                with: #require(provider["client_configuration"] as? Data)
            ) as? [String: Any]
        )
        client["webrtc_signaling_url"] = "https://vpn.example.com/private/webrtc/offer"
        provider["client_configuration"] = try JSONSerialization.data(withJSONObject: client)

        #expect(throws: StrictPacketTunnelConfigurationError.alternateCarrierConfigured) {
            try StrictPacketTunnelConfiguration(
                providerConfiguration: provider,
                credentialReference: Data([0x01])
            )
        }
    }

    @Test("rejects oversized or malformed platform inputs")
    func rejectsUnsafeBounds() throws {
        var oversized = try validProviderConfiguration()
        oversized["client_routes"] = Data(
            repeating: 0x20,
            count: StrictPacketTunnelConfiguration.maximumClientRoutesBytes + 1
        )
        #expect(throws: StrictPacketTunnelConfigurationError.invalidClientRoutes) {
            try StrictPacketTunnelConfiguration(
                providerConfiguration: oversized,
                credentialReference: Data([0x01])
            )
        }

        let valid = try validProviderConfiguration()
        #expect(throws: StrictPacketTunnelConfigurationError.invalidCredentialReference) {
            try StrictPacketTunnelConfiguration(
                providerConfiguration: valid,
                credentialReference: Data()
            )
        }
    }

    private func validProviderConfiguration() throws -> [String: Any] {
        let profile = ServerProfile(
            id: UUID(uuidString: "79D6AC07-A320-42D7-8F8F-1B8576EE7BD1")!,
            name: "Primary",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            http3Path: "/private/http3/session",
            maxParallelCarriers: 3,
            coverProfile: .web
        )
        return [
            "profile_id": profile.id.uuidString.lowercased(),
            "client_configuration": try profile.strictHTTP3ClientConfigurationJSON(),
            "client_routes": Data("[]".utf8),
            "carrier_policy": "http3-only",
        ]
    }
}
