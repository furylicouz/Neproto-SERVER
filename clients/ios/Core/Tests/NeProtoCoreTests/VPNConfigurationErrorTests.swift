import Foundation
import Testing
@testable import NeProtoCore

@Suite("VPN configuration errors")
struct VPNConfigurationErrorTests {
    @Test("both NetworkExtension stale error variants request a retry")
    func recognizesStaleErrors() {
        let managerStale = NSError(domain: "NEVPNErrorDomain", code: 4)
        let preferencesStale = NSError(domain: "NEConfigurationErrorDomain", code: 5)

        #expect(VPNConfigurationError.isStale(managerStale))
        #expect(VPNConfigurationError.isStale(preferencesStale))
    }

    @Test("unrelated errors are not retried")
    func rejectsUnrelatedErrors() {
        let permissionDenied = NSError(domain: "NEConfigurationErrorDomain", code: 10)
        let arbitraryError = NSError(domain: NSCocoaErrorDomain, code: 5)

        #expect(!VPNConfigurationError.isStale(permissionDenied))
        #expect(!VPNConfigurationError.isStale(arbitraryError))
    }
}
