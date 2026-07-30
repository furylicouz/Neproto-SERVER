import Foundation

public struct ClusterCatalogRefreshGate: Sendable {
    private struct Attempt: Sendable {
        var sessionID: UInt64
        var isInFlight: Bool
        var isSynchronized: Bool
        var retryNotBefore: Date?
    }

    private var attempts: [UUID: Attempt] = [:]
    private let retryDelay: TimeInterval

    public init(retryDelay: TimeInterval = 30) {
        self.retryDelay = max(1, retryDelay)
    }

    public mutating func shouldStart(profileID: UUID, sessionID: UInt64, now: Date = .now) -> Bool {
        if let attempt = attempts[profileID], attempt.sessionID == sessionID {
            guard !attempt.isInFlight, !attempt.isSynchronized else { return false }
            if let retryNotBefore = attempt.retryNotBefore, retryNotBefore > now { return false }
        }

        attempts[profileID] = Attempt(
            sessionID: sessionID,
            isInFlight: true,
            isSynchronized: false,
            retryNotBefore: nil
        )
        return true
    }

    public mutating func finish(
        profileID: UUID,
        sessionID: UInt64,
        succeeded: Bool,
        now: Date = .now
    ) {
        guard var attempt = attempts[profileID], attempt.sessionID == sessionID else { return }
        attempt.isInFlight = false
        attempt.isSynchronized = succeeded
        attempt.retryNotBefore = succeeded ? nil : now.addingTimeInterval(retryDelay)
        attempts[profileID] = attempt
    }

    public mutating func reset(profileID: UUID) {
        attempts.removeValue(forKey: profileID)
    }
}
