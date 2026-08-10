import Foundation

/// Persists a random application-installation identity. The value is not a
/// hardware identifier and is deliberately shared by every NP/2 profile in the
/// same app installation so a carrier pool or cluster does not consume extra
/// device slots.
public struct InstallationIdentityStore {
    private static let defaultKey = "neproto.np2.installation-device-id"

    private let defaults: UserDefaults
    private let key: String

    public init(defaults: UserDefaults = .standard, key: String? = nil) {
        self.defaults = defaults
        self.key = key ?? Self.defaultKey
    }

    public func identifier() -> UUID {
        if let raw = defaults.string(forKey: key),
           let existing = UUID(uuidString: raw),
           existing != Self.zeroUUID {
            return existing
        }
        let generated = UUID()
        defaults.set(generated.uuidString.lowercased(), forKey: key)
        return generated
    }

    private static let zeroUUID = UUID(uuidString: "00000000-0000-0000-0000-000000000000")!
}
