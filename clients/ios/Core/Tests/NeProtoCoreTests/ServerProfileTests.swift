import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 server profile")
struct ServerProfileTests {
    @Test("valid profile emits strict Go configuration without secret material")
    func emitsStrictConfigurationWithoutSecret() throws {
        let profile = ServerProfile(
            id: UUID(uuidString: "79D6AC07-A320-42D7-8F8F-1B8576EE7BD1")!,
            name: "Primary",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            http3Path: "/private/http3/session",
            requireDatagrams: true,
            clusterID: "cluster-01",
            catalogPublicKey: Data(repeating: 0x44, count: 32).base64URLEncodedString(),
            coverProfile: .interactive
        )
        let secret = Data(repeating: 0x5A, count: 32).base64URLEncodedString()

        try profile.validate(secret: secret)
        let deviceID = UUID(uuidString: "10223344-5566-7788-99AA-BBCCDDEEF001")!
        let raw = try profile.clientConfigurationJSON(deviceID: deviceID)
        let object = try #require(JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let rawString = String(decoding: raw, as: UTF8.self)

        #expect(object["server_identity"] as? String == "vpn.example.com")
		#expect(object["device_id"] as? String == "10223344-5566-7788-99aa-bbccddeef001")
        #expect(object["server_addresses"] as? [String] == ["8.8.8.8"])
        #expect(object["https_url"] as? String == "wss://vpn.example.com/private/https/session")
        #expect(object["carrier_policy"] as? String == "performance")
        #expect(object["webrtc_signaling_url"] as? String == "https://vpn.example.com/private/webrtc/offer")
        #expect(object["http3_url"] as? String == "https://vpn.example.com/private/http3/session")
        #expect(object["http3_timeout"] as? String == "5s")
        #expect(object["initial_window_bytes"] as? Int == 2_097_152)
        #expect(object["max_parallel_carriers"] as? Int == 3)
        #expect(object["require_datagrams"] as? Bool == true)
        #expect(object["enable_constellation"] as? Bool == true)
        #expect(object["enable_forward_secrecy"] as? Bool == true)
        #expect(object["max_cover_overhead_percent"] as? Int == 30)
        #expect(object["profile"] as? String == CoverProfile.web.rawValue)
        #expect(object["socks_listen"] == nil)
        #expect(object["max_socks_connections"] == nil)
        #expect(object["secret_file"] as? String == "keychain")
        #expect(!rawString.contains(secret))
        #expect(!rawString.lowercased().contains("password"))
        #expect(profile.clusterID == "cluster-01")
    }

	@Test("cross-platform candidate adapts a stored profile to HTTP/3 only")
	func emitsHTTP3OnlyCandidateConfiguration() throws {
		let profile = ServerProfile(
			name: "Primary",
			serverIdentity: "vpn.example.com",
			serverAddress: "8.8.8.8",
			httpsPath: "/private/https/session",
			webRTCPath: "/private/webrtc/offer",
			http3Path: "/private/http3/session",
			maxParallelCarriers: 3,
			coverProfile: .web
		)

		let raw = try profile.strictHTTP3ClientConfigurationJSON()
		let object = try #require(JSONSerialization.jsonObject(with: raw) as? [String: Any])
		#expect(object["carrier_policy"] as? String == "http3-only")
		#expect(object["max_parallel_carriers"] as? Int == 1)
		#expect(object["http3_url"] as? String == "https://vpn.example.com/private/http3/session")
		#expect(object["https_url"] == nil)
		#expect(object["https_timeout"] == nil)
		#expect(object["webrtc_signaling_url"] == nil)
		#expect(object["webrtc_timeout"] == nil)
		#expect(profile.maxParallelCarriers == 3)
	}

	@Test("HTTP/3-only candidate rejects a legacy profile without HTTP/3")
	func rejectsCandidateWithoutHTTP3() {
		let profile = ServerProfile(
			name: "Legacy",
			serverIdentity: "vpn.example.com",
			serverAddress: "8.8.8.8",
			httpsPath: "/private/https/session",
			webRTCPath: "/private/webrtc/offer",
			coverProfile: .web
		)
		#expect(throws: ProfileValidationError.invalidHTTP3Path) {
			try profile.strictHTTP3ClientConfigurationJSON()
		}
	}

	@Test("iOS A/B candidate adapts a stored profile to HTTPS only")
	func emitsHTTPSOnlyCandidateConfiguration() throws {
		let profile = ServerProfile(
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

		let raw = try profile.strictHTTPSClientConfigurationJSON()
		let object = try #require(JSONSerialization.jsonObject(with: raw) as? [String: Any])
		#expect(object["carrier_policy"] as? String == "https-only")
		#expect(object["max_parallel_carriers"] as? Int == 1)
		#expect(object["https_url"] as? String == "wss://vpn.example.com/private/https/session")
		#expect(object["https_timeout"] as? String == "10s")
		#expect(object["require_datagrams"] as? Bool == false)
		#expect(object["http3_url"] == nil)
		#expect(object["http3_timeout"] == nil)
		#expect(object["webrtc_signaling_url"] == nil)
		#expect(object["webrtc_timeout"] == nil)
		#expect(profile.requireDatagrams)
		#expect(profile.maxParallelCarriers == 3)
	}

    @Test("provider payload round-trips without credentials")
    func providerPayloadRoundTrips() throws {
        let profile = ServerProfile(
            name: "Mobile",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            coverProfile: .web
        )

        let payload = try profile.providerPayload()
        let decoded = try ServerProfile(providerPayload: payload)

        #expect(decoded == profile)
        #expect(!String(decoding: payload, as: UTF8.self).contains("secret"))
    }

    @Test("missing pinned endpoint is never replaced with a baked-in server")
    func rejectsMissingPinnedEndpoint() throws {
        let profile = ServerProfile(
            name: "Legacy",
            serverIdentity: "neproto.lyntragram.ru",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            coverProfile: .interactive
        )
        let secret = Data(repeating: 0x5A, count: 32).base64URLEncodedString()
        #expect(throws: ProfileValidationError.self) {
            try profile.validate(secret: secret)
        }
    }

    @Test("provider payload from v1 defaults new NP/2.2 fields safely")
    func decodesLegacyProviderPayload() throws {
        let profile = ServerProfile(
            name: "Legacy",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            coverProfile: .web
        )
        var object = try #require(JSONSerialization.jsonObject(with: profile.providerPayload()) as? [String: Any])
        object.removeValue(forKey: "requireDatagrams")
        object.removeValue(forKey: "http3Path")
        object.removeValue(forKey: "maxParallelCarriers")
        object.removeValue(forKey: "enableConstellation")
        object.removeValue(forKey: "enableForwardSecrecy")
        object.removeValue(forKey: "clusterID")
        object.removeValue(forKey: "catalogPublicKey")
        object.removeValue(forKey: "clusterNodeID")
        object.removeValue(forKey: "managedByCluster")
        let legacyPayload = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        let decoded = try ServerProfile(providerPayload: legacyPayload)
        #expect(decoded.http3Path == nil)
        #expect(decoded.requireDatagrams == false)
        #expect(decoded.maxParallelCarriers == 3)
        #expect(decoded.enableConstellation == false)
        #expect(decoded.enableForwardSecrecy == false)
    }

    @Test("Fake-IP cannot become a carrier bypass route")
    func rejectsFakeIPAddress() {
        let profile = ServerProfile(
            name: "Unsafe",
            serverIdentity: "vpn.example.com",
            serverAddress: "198.18.1.233",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            coverProfile: .interactive
        )
        let secret = Data(repeating: 0x5A, count: 32).base64URLEncodedString()
        #expect(throws: ProfileValidationError.self) {
            try profile.validate(secret: secret)
        }
    }

    @Test(
        "unsafe profile inputs are rejected",
        arguments: [
            ("VPN.EXAMPLE.COM", "/private/https/session", "/private/webrtc/offer"),
            ("vpn.example.com", "/short", "/private/webrtc/offer"),
            ("vpn.example.com", "/private/same/session", "/private/same/session"),
            ("vpn.example.com", "/private//session", "/private/webrtc/offer"),
            ("vpn.example.com", "/private/../session", "/private/webrtc/offer"),
        ]
    )
    func rejectsUnsafeProfile(identity: String, httpsPath: String, webRTCPath: String) {
        let profile = ServerProfile(
            name: "Unsafe",
            serverIdentity: identity,
            serverAddress: "8.8.8.8",
            httpsPath: httpsPath,
            webRTCPath: webRTCPath,
            coverProfile: .quiet
        )
        let secret = Data(repeating: 0x5A, count: 32).base64URLEncodedString()

        #expect(throws: ProfileValidationError.self) {
            try profile.validate(secret: secret)
        }
    }

    @Test("noncanonical and zero root secrets are rejected")
    func rejectsUnsafeSecrets() {
        let profile = ServerProfile(
            name: "Primary",
            serverIdentity: "vpn.example.com",
            serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session",
            webRTCPath: "/private/webrtc/offer",
            coverProfile: .interactive
        )

        for secret in ["short", Data(repeating: 0, count: 32).base64URLEncodedString(), Data(repeating: 1, count: 32).base64EncodedString()] {
            #expect(throws: ProfileValidationError.self) {
                try profile.validate(secret: secret)
            }
        }
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
