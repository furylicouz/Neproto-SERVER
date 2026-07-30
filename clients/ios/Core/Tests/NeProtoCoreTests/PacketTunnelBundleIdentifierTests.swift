import Testing
@testable import NeProtoCore

@Suite("Packet Tunnel bundle identifier")
struct PacketTunnelBundleIdentifierTests {
    @Test("provider identifier follows the signed application identifier")
    func derivesProviderIdentifier() {
        #expect(PacketTunnelBundleIdentifier.derive(from: "com.neproto.ios") == "com.neproto.ios.PacketTunnel")
        #expect(PacketTunnelBundleIdentifier.derive(from: "ru.neproto.ios") == "ru.neproto.ios.PacketTunnel")
    }

    @Test("missing application identifiers are rejected")
    func rejectsMissingIdentifier() {
        #expect(PacketTunnelBundleIdentifier.derive(from: nil) == nil)
        #expect(PacketTunnelBundleIdentifier.derive(from: "") == nil)
    }
}
