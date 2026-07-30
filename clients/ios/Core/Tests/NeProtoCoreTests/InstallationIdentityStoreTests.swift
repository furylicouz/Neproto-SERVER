import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 installation identity")
struct InstallationIdentityStoreTests {
    @Test("one random identity is reused by every profile in an installation")
    func reusesIdentityAcrossStoreInstances() throws {
        let suite = "NeProtoCoreTests.InstallationIdentity.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        let first = InstallationIdentityStore(defaults: defaults).identifier()
        let second = InstallationIdentityStore(defaults: defaults).identifier()

        #expect(first == second)
        #expect(first != UUID(uuidString: "00000000-0000-0000-0000-000000000000"))
    }
}
