import Testing
@testable import NeProtoCore

@Test func userInitiatedDisconnectDoesNotProduceAnErrorAlert() {
    #expect(!VPNDisconnectPolicy.shouldReportError(wasUserInitiated: true))
}

@Test func unexpectedDisconnectStillProducesDiagnostics() {
    #expect(VPNDisconnectPolicy.shouldReportError(wasUserInitiated: false))
}
