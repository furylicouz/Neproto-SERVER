import Testing
@testable import NeProtoCore

struct ServerLatencyPresentationTests {
    @Test func unavailableLatencyHasNoBarsAndUsesDash() {
        #expect(ServerLatencyPresentation.bars(milliseconds: nil) == 0)
        #expect(ServerLatencyPresentation.text(milliseconds: nil) == "—")
    }

    @Test(arguments: [
        (milliseconds: 50, bars: 4),
        (milliseconds: 80, bars: 4),
        (milliseconds: 81, bars: 3),
        (milliseconds: 160, bars: 3),
        (milliseconds: 161, bars: 2),
        (milliseconds: 300, bars: 2),
        (milliseconds: 301, bars: 1),
    ])
    func latencyMapsToStableSignalBuckets(milliseconds: Int, bars: Int) {
        #expect(ServerLatencyPresentation.bars(milliseconds: milliseconds) == bars)
        #expect(ServerLatencyPresentation.text(milliseconds: milliseconds) == "\(milliseconds) мс")
    }

    @Test func negativeLatencyIsTreatedAsUnavailable() {
        #expect(ServerLatencyPresentation.bars(milliseconds: -1) == 0)
        #expect(ServerLatencyPresentation.text(milliseconds: -1) == "—")
    }
}
