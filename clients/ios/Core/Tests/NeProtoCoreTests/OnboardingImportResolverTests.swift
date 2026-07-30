import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 onboarding import resolution")
struct OnboardingImportResolverTests {
    @Test("cluster QR upgrades an existing legacy credential in place")
    func upgradesLegacyCredentialInPlace() throws {
        let existingID = UUID(uuidString: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE")!
        let existing = ServerProfile(
            id: existingID,
            credentialID: credentialID,
            name: "Legacy",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/legacy/https/session",
            webRTCPath: "/legacy/webrtc/offer",
            coverProfile: .web
        )

        let resolution = try OnboardingImportResolver.resolve(
            existing: [existing],
            onboarding: try clusterOnboarding()
        )

        #expect(resolution.replacedProfileID == existingID)
        #expect(resolution.profile.id == existingID)
        #expect(resolution.profile.clusterID == "cluster-01")
        #expect(resolution.profile.catalogPublicKey == catalogKey)
        #expect(resolution.profile.http3Path == "/private/http3/session")
    }

    @Test("an unpinned duplicate QR cannot overwrite an existing credential")
    func rejectsUnpinnedDuplicate() throws {
        let existing = ServerProfile(
            credentialID: credentialID,
            name: "Existing",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/legacy/https/session",
            webRTCPath: "/legacy/webrtc/offer",
            coverProfile: .web
        )

        #expect(throws: OnboardingImportResolutionError.duplicateCredential) {
            try OnboardingImportResolver.resolve(
                existing: [existing],
                onboarding: try legacyOnboarding()
            )
        }
    }

    @Test("a different pinned cluster cannot replace an existing cluster profile")
    func rejectsClusterIdentityReplacement() throws {
        let existing = ServerProfile(
            credentialID: credentialID,
            name: "Existing",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/legacy/https/session",
            webRTCPath: "/legacy/webrtc/offer",
            clusterID: "other-cluster",
            catalogPublicKey: Data(repeating: 0x55, count: 32).base64URLEncodedString(),
            coverProfile: .web
        )

        #expect(throws: OnboardingImportResolutionError.clusterIdentityMismatch) {
            try OnboardingImportResolver.resolve(
                existing: [existing],
                onboarding: try clusterOnboarding()
            )
        }
    }

    private var credentialID: String { "ABEiM0RVZneImaq7zN3u_w" }
    private var secret: String { Data(repeating: 0x5A, count: 32).base64URLEncodedString() }
    private var catalogKey: String { Data(repeating: 0x44, count: 32).base64URLEncodedString() }

    private func clusterOnboarding() throws -> OnboardingProfile {
        try onboarding(
            version: 2,
            prefix: OnboardingProfile.prefix,
            extra: [
                "http3_path": "/private/http3/session",
                "max_parallel_carriers": 3,
                "enable_constellation": true,
                "enable_forward_secrecy": true,
                "cluster_id": "cluster-01",
                "catalog_public_key": catalogKey,
            ]
        )
    }

    private func legacyOnboarding() throws -> OnboardingProfile {
        try onboarding(version: 1, prefix: OnboardingProfile.legacyPrefix, extra: [:])
    }

    private func onboarding(version: Int, prefix: String, extra: [String: Any]) throws -> OnboardingProfile {
        var object: [String: Any] = [
            "version": version,
            "credential_id": credentialID,
            "name": "Updated",
            "server_identity": "vpn.example.com",
            "server_addresses": ["8.8.8.8"],
            "https_path": "/private/https/session",
            "webrtc_path": "/private/webrtc/offer",
            "profile": "interactive",
            "secret": secret,
        ]
        object.merge(extra) { _, replacement in replacement }
        let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        return try OnboardingProfile(uri: prefix + data.base64URLEncodedString())
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
