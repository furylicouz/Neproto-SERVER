import Foundation
import Testing
@testable import NeProtoCore

@Suite("Cluster catalog refresh gate")
struct ClusterCatalogRefreshGateTests {
    private let profileID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")!
    private let now = Date(timeIntervalSince1970: 1_700_000_000)

    @Test("one connected session starts at most one successful refresh")
    func oneRefreshPerConnectedSession() {
        var gate = ClusterCatalogRefreshGate()

        let firstStart = gate.shouldStart(profileID: profileID, sessionID: 7, now: now)
        let duplicateStart = gate.shouldStart(profileID: profileID, sessionID: 7, now: now)
        #expect(firstStart)
        #expect(!duplicateStart)

        gate.finish(profileID: profileID, sessionID: 7, succeeded: true, now: now)

        let completedSessionStart = gate.shouldStart(
            profileID: profileID, sessionID: 7, now: now.addingTimeInterval(120)
        )
        let newSessionStart = gate.shouldStart(
            profileID: profileID, sessionID: 8, now: now.addingTimeInterval(120)
        )
        #expect(!completedSessionStart)
        #expect(newSessionStart)
    }

    @Test("failed refresh is bounded by retry delay")
    func failedRefreshBacksOff() {
        var gate = ClusterCatalogRefreshGate(retryDelay: 30)

        let initialStart = gate.shouldStart(profileID: profileID, sessionID: 9, now: now)
        #expect(initialStart)
        gate.finish(profileID: profileID, sessionID: 9, succeeded: false, now: now)

        let earlyRetry = gate.shouldStart(
            profileID: profileID, sessionID: 9, now: now.addingTimeInterval(29)
        )
        let allowedRetry = gate.shouldStart(
            profileID: profileID, sessionID: 9, now: now.addingTimeInterval(30)
        )
        #expect(!earlyRetry)
        #expect(allowedRetry)
    }

    @Test("stale completion cannot close a newer session attempt")
    func staleCompletionIsIgnored() {
        var gate = ClusterCatalogRefreshGate()

        let oldSessionStart = gate.shouldStart(profileID: profileID, sessionID: 10, now: now)
        let newSessionStart = gate.shouldStart(profileID: profileID, sessionID: 11, now: now)
        #expect(oldSessionStart)
        #expect(newSessionStart)
        gate.finish(profileID: profileID, sessionID: 10, succeeded: true, now: now)

        let duplicateStart = gate.shouldStart(profileID: profileID, sessionID: 11, now: now)
        #expect(!duplicateStart)
        gate.finish(profileID: profileID, sessionID: 11, succeeded: true, now: now)
        let completedSessionStart = gate.shouldStart(
            profileID: profileID, sessionID: 11, now: now.addingTimeInterval(120)
        )
        #expect(!completedSessionStart)
    }
}
