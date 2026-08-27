import Testing
@testable import NeProtoCore

@Suite("Strict network migration gate")
struct StrictNetworkMigrationGateTests {
    @Test("coalesces path flapping into one active and one pending reconnect")
    func coalescesPathFlapping() {
        var gate = StrictNetworkMigrationGate()

		let firstChange = gate.pathChanged()
		#expect(firstChange)
        for _ in 0..<100 {
			let queued = gate.pathChanged()
			#expect(!queued)
        }
		let replay = gate.completed()
		#expect(replay)
		let drained = gate.completed()
		#expect(!drained)
		let nextChange = gate.pathChanged()
		#expect(nextChange)
    }

    @Test("reset discards pending work during tunnel teardown")
    func resetsPendingWork() {
        var gate = StrictNetworkMigrationGate()
		let firstChange = gate.pathChanged()
		#expect(firstChange)
		let queued = gate.pathChanged()
		#expect(!queued)

        gate.reset()

		let drained = gate.completed()
		#expect(!drained)
		let nextChange = gate.pathChanged()
		#expect(nextChange)
    }
}
