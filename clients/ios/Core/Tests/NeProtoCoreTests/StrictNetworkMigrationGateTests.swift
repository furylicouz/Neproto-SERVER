import Testing
@testable import NeProtoCore

@Suite("Strict network migration gate")
struct StrictNetworkMigrationGateTests {
    @Test("coalesces path flapping into one active and one pending reconnect")
    func coalescesPathFlapping() {
        var gate = StrictNetworkMigrationGate()

        #expect(gate.pathChanged())
        for _ in 0..<100 {
            #expect(!gate.pathChanged())
        }
        #expect(gate.completed())
        #expect(!gate.completed())
        #expect(gate.pathChanged())
    }

    @Test("reset discards pending work during tunnel teardown")
    func resetsPendingWork() {
        var gate = StrictNetworkMigrationGate()
        #expect(gate.pathChanged())
        #expect(!gate.pathChanged())

        gate.reset()

        #expect(!gate.completed())
        #expect(gate.pathChanged())
    }
}
