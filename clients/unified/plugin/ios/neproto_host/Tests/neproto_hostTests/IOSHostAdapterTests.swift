import Foundation
import NeProtoCore
import NetworkExtension
import Security
import Testing
@testable import neproto_host

@Suite("iOS host adapter")
@MainActor
struct IOSHostAdapterTests {
  @Test("legacy profile storage is read through without rewrite")
  func legacyReadThroughIsByteStable() throws {
    let suite = "neproto-host-tests-\(UUID().uuidString)"
    let defaults = try #require(UserDefaults(suiteName: suite))
    defer { defaults.removePersistentDomain(forName: suite) }
    let profile = makeProfile()
    let original = try JSONEncoder().encode([profile])
    defaults.set(original, forKey: IOSProfileRepository.profileStorageKey)
    let credentials = FakeCredentialStore()
    credentials.values[profile.id] = Data([1, 2, 3])

    let first = IOSProfileRepository(defaults: defaults, credentials: credentials)
    let second = IOSProfileRepository(defaults: defaults, credentials: credentials)

    #expect(first.summaries().count == 1)
    #expect(first.summaries()[0].hasCredential)
    #expect(first.selectedProfileID == profile.id)
    #expect(second.selectedProfileID == profile.id)
    #expect(defaults.data(forKey: IOSProfileRepository.profileStorageKey) == original)
    #expect(defaults.string(forKey: IOSProfileRepository.selectedProfileKey) == nil)
  }

  @Test("provider configuration contains strict HTTPS but no credential")
  func strictProviderConfigurationIsSecretFree() throws {
    let suite = "neproto-host-tests-\(UUID().uuidString)"
    let defaults = try #require(UserDefaults(suiteName: suite))
    defer { defaults.removePersistentDomain(forName: suite) }
    let profile = makeProfile()
    defaults.set(try JSONEncoder().encode([profile]), forKey: IOSProfileRepository.profileStorageKey)
    let credentials = FakeCredentialStore()
    let repository = IOSProfileRepository(defaults: defaults, credentials: credentials)

    let configuration = try repository.strictProviderConfiguration(profile: profile)
    let raw = try #require(configuration["client_configuration"] as? Data)
    let object = try #require(JSONSerialization.jsonObject(with: raw) as? [String: Any])
    #expect(object["carrier_policy"] as? String == "https-only")
    #expect(object["max_parallel_carriers"] as? Int == 1)
	#expect(object["https_url"] as? String == "wss://vpn.example.com/private/https/session")
	#expect(object["http3_url"] == nil)
	#expect(object["webrtc_signaling_url"] == nil)
    #expect(configuration["secret"] == nil)
    #expect(configuration["credential"] == nil)
  }

  @Test("NetworkExtension states map fail closed")
  func mapsVPNStates() {
    #expect(IOSVPNStatusMapper.state(.invalid) == .disconnected)
    #expect(IOSVPNStatusMapper.state(.connecting) == .connecting)
    #expect(IOSVPNStatusMapper.state(.connected) == .connected)
    #expect(IOSVPNStatusMapper.state(.reasserting) == .reconnecting)
    #expect(IOSVPNStatusMapper.state(.disconnecting) == .disconnecting)
    #expect(IOSVPNStatusMapper.carrier(.connected) == .httpsWebSocket)
    #expect(IOSVPNStatusMapper.carrier(.connecting) == .none)
    #expect(IOSVPNStatusMapper.carrier(.failed) == .none)
  }

  @Test("packet tunnel protocol enforces routes over app interface scoping")
  func enforcesPacketTunnelRoutes() {
    let profile = makeProfile()
    let configuration = ["carrier_policy": "https-only"]
    let reference = Data([0x01, 0x02, 0x03])

    let tunnelProtocol = IOSVPNProtocolFactory.make(
      profile: profile,
      providerConfiguration: configuration,
      credentialReference: reference,
      providerBundleID: "com.neproto.ios.PacketTunnel"
    )

    #expect(tunnelProtocol.enforceRoutes)
    #expect(!tunnelProtocol.includeAllNetworks)
    let policy = tunnelProtocol.providerConfiguration?["carrier_policy"] as? String
    #expect(policy == "https-only")
    #expect(tunnelProtocol.passwordReference == reference)
  }

  @Test("profile import failures keep their stable boundary")
  func classifiesProfileImportFailures() {
    let invalid = IOSClientHost.importFailure(for: OnboardingProfileError.malformedPayload)
    #expect(invalid.pigeonCode == "INVALID_PROFILE")
    #expect(invalid.diagnosticCode == .invalidProfile)
    #expect(invalid.stage == .profileValidation)

    let credential = IOSClientHost.importFailure(
      for: KeychainSecretStoreError.unexpectedStatus(errSecMissingEntitlement)
    )
    #expect(credential.pigeonCode == "CREDENTIAL_UNAVAILABLE")
    #expect(credential.diagnosticCode == .credentialUnavailable)
    #expect(credential.stage == .credentialLoad)

    let unexpected = IOSClientHost.importFailure(for: ImportProbeError())
    #expect(unexpected.pigeonCode == "INTERNAL")
    #expect(unexpected.diagnosticCode == .internalFailure)
    #expect(unexpected.stage == .hostIpc)
  }

  @Test("packet tunnel runtime health is bounded and destination free")
  func parsesPacketTunnelRuntimeHealth() throws {
    let raw = Data(#"{"state":"connected","carrier":"https_websocket","upload_bytes_per_second":125000,"download_bytes_per_second":250000,"upload_total_bytes":1000000,"download_total_bytes":2000000,"quic_smoothed_rtt_ms":0,"quic_packets_sent":0,"quic_packets_lost":0,"quic_bytes_sent":0,"quic_bytes_lost":0}"#.utf8)
    let health = try #require(IOSRuntimeHealth.decode(raw))
    #expect(health.downloadBytesPerSecond == 250_000)
	#expect(health.carrier == "https_websocket")
	#expect(health.diagnosticMessage.contains("HTTPS/TLS health"))
	#expect(!health.diagnosticMessage.contains("RTT"))
	#expect(!health.diagnosticMessage.contains("packet loss"))
    #expect(!health.diagnosticMessage.contains("telegram"))

    #expect(IOSRuntimeHealth.decode(Data(repeating: 0x41, count: 16_385)) == nil)
    #expect(IOSRuntimeHealth.decode(Data(#"{"quic_packets_lost":2,"quic_packets_sent":1}"#.utf8)) == nil)
  }

  private func makeProfile() -> ServerProfile {
    ServerProfile(
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
  }
}

private struct ImportProbeError: Error {}

@MainActor
private final class FakeCredentialStore: IOSCredentialStore {
  var values: [UUID: Data] = [:]

  func save(secret: String, profileID: UUID) throws -> Data {
    let reference = Data(secret.utf8.prefix(16))
    values[profileID] = reference
    return reference
  }

  func persistentReference(profileID: UUID) throws -> Data {
    guard let value = values[profileID] else { throw KeychainSecretStoreError.itemNotFound }
    return value
  }

  func read(profileID: UUID) throws -> String {
    guard values[profileID] != nil else { throw KeychainSecretStoreError.itemNotFound }
    return Data(repeating: 0x42, count: 32).base64EncodedString()
  }

  func delete(profileID: UUID) throws {
    guard values.removeValue(forKey: profileID) != nil else {
      throw KeychainSecretStoreError.itemNotFound
    }
  }
}
