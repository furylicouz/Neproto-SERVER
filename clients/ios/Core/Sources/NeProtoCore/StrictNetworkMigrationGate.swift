/// Coalesces an arbitrary number of OS path notifications into at most one
/// active and one pending same-carrier reconnect operation.
public struct StrictNetworkMigrationGate: Sendable {
    private var active = false
    private var pending = false

    public init() {}

    /// Returns true only when the caller must start a reconnect operation.
    public mutating func pathChanged() -> Bool {
        if active {
            pending = true
            return false
        }
        active = true
        return true
    }

    /// Returns true when one coalesced follow-up operation must be started.
    public mutating func completed() -> Bool {
        if pending {
            pending = false
            return true
        }
        active = false
        return false
    }

    public mutating func reset() {
        active = false
        pending = false
    }
}
