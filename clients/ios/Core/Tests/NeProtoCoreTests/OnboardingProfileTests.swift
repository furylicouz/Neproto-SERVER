import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 onboarding profile")
struct OnboardingProfileTests {
    @Test("v2 QR imports HTTP/3 and datagram policy")
    func importsProductionV2Profile() throws {
        let object: [String: Any] = [
            "version": 2,
            "credential_id": "ABEiM0RVZneImaq7zN3u_w",
            "name": "Production iPhone",
            "region": "Russia",
            "server_identity": "vpn.example.com",
            "server_addresses": ["8.8.8.8"],
            "https_path": "/private/https/session",
            "webrtc_path": "/private/webrtc/offer",
            "http3_path": "/private/http3/session",
            "require_datagrams": true,
            "max_parallel_carriers": 3,
            "enable_constellation": true,
            "enable_forward_secrecy": true,
            "cluster_id": "cluster-01",
            "catalog_public_key": Data(repeating: 0x44, count: 32).base64URLEncodedString(),
            "profile": "interactive",
            "secret": Data(repeating: 0x5A, count: 32).base64URLEncodedString(),
        ]
        let uri = try makeURI(prefix: OnboardingProfile.prefix, object: object)
        let onboarding = try OnboardingProfile(uri: uri)
        let profile = try onboarding.serverProfile()
        #expect(profile.http3Path == "/private/http3/session")
        #expect(profile.requireDatagrams)
        #expect(profile.region == "Russia")
        #expect(profile.maxParallelCarriers == 3)
        #expect(profile.enableConstellation)
        #expect(profile.enableForwardSecrecy)
        #expect(profile.clusterID == "cluster-01")
        #expect(profile.catalogPublicKey == Data(repeating: 0x44, count: 32).base64URLEncodedString())
    }

    @Test("v1 QR remains importable without HTTP/3")
    func importsLegacyV1Profile() throws {
        let object: [String: Any] = [
            "version": 1,
            "credential_id": "ABEiM0RVZneImaq7zN3u_w",
            "name": "Legacy iPhone",
            "server_identity": "vpn.example.com",
            "server_addresses": ["8.8.8.8"],
            "https_path": "/private/https/session",
            "webrtc_path": "/private/webrtc/offer",
            "profile": "web",
            "secret": Data(repeating: 0x5A, count: 32).base64URLEncodedString(),
        ]
        let uri = try makeURI(prefix: OnboardingProfile.legacyPrefix, object: object)
        let profile = try OnboardingProfile(uri: uri).serverProfile()
        #expect(profile.http3Path == nil)
        #expect(!profile.requireDatagrams)
        #expect(profile.maxParallelCarriers == 3)
        #expect(!profile.enableConstellation)
        #expect(!profile.enableForwardSecrecy)
    }

    private func makeURI(prefix: String, object: [String: Any]) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        return prefix + data.base64URLEncodedString()
    }
}

private extension Data {
    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
