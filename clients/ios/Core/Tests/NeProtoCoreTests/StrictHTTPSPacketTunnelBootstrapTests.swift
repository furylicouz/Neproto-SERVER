import Foundation
import Testing
@testable import NeProtoCore

@Suite("Strict HTTPS Packet Tunnel bootstrap")
struct StrictHTTPSPacketTunnelBootstrapTests {
    @Test("converts the persisted profile into one HTTPS-only immutable snapshot")
    func createsStrictHTTPSSnapshot() throws {
        let profile = ServerProfile(
            id: UUID(uuidString: "79D6AC07-A320-42D7-8F8F-1B8576EE7BD1")!,
            name: "Primary",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            http3Path: "/private/http3/session",
            requireDatagrams: true,
            maxParallelCarriers: 3,
            coverProfile: .web
        )
        let deviceID = UUID(uuidString: "10223344-5566-7788-99AA-BBCCDDEEF001")!
        let reference = Data([0x01, 0x02, 0x03])
        let bootstrap = try StrictHTTPSPacketTunnelBootstrap(
            providerConfiguration: [
                "profile_id": profile.id.uuidString.lowercased(),
                "device_id": deviceID.uuidString.lowercased(),
                "profile_payload": try profile.providerPayload(),
                "client_routes": Data("[]".utf8),
            ],
            credentialReference: reference
        )

        let raw = Data(bootstrap.clientConfigurationJSON.utf8)
        let object = try #require(JSONSerialization.jsonObject(with: raw) as? [String: Any])
        #expect(bootstrap.profileID == profile.id.uuidString.lowercased())
        #expect(bootstrap.clientRoutesJSON == "[]")
        #expect(bootstrap.credentialReference == reference)
        #expect(object["carrier_policy"] as? String == "https-only")
        #expect(object["https_url"] as? String == "wss://vpn.example.com/private/https/session")
        #expect(object["max_parallel_carriers"] as? Int == 1)
        #expect(object["require_datagrams"] as? Bool == false)
        #expect(object["cover_mode"] as? String == "off")
        #expect(object["http3_url"] == nil)
        #expect(object["webrtc_signaling_url"] == nil)
    }

    @Test("rejects malformed platform inputs before starting the HTTPS native core")
    func rejectsMalformedInputs() throws {
        let profile = ServerProfile(
            name: "Primary",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            http3Path: "/private/http3/session",
            coverProfile: .web
        )
        let valid: [String: Any] = [
            "profile_id": profile.id.uuidString.lowercased(),
            "device_id": UUID().uuidString.lowercased(),
            "profile_payload": try profile.providerPayload(),
            "client_routes": Data("[]".utf8),
        ]

        #expect(throws: StrictHTTPSPacketTunnelBootstrapError.invalidCredentialReference) {
            try StrictHTTPSPacketTunnelBootstrap(
                providerConfiguration: valid,
                credentialReference: Data()
            )
        }

        var malformedRoutes = valid
        malformedRoutes["client_routes"] = Data("{}".utf8)
        #expect(throws: StrictHTTPSPacketTunnelBootstrapError.invalidClientRoutes) {
            try StrictHTTPSPacketTunnelBootstrap(
                providerConfiguration: malformedRoutes,
                credentialReference: Data([0x01])
            )
        }
    }
}
