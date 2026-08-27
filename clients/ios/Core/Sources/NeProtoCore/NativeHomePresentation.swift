import Foundation

public struct NativeSubscriptionSection: Identifiable, Equatable, Sendable {
    public let id: String
    public var title: String
    public var profiles: [ServerProfile]

    public init(id: String, title: String, profiles: [ServerProfile]) {
        self.id = id
        self.title = title
        self.profiles = profiles
    }
}

public struct NativeSubscriptionDisclosureState: Equatable, Sendable {
    private var collapsedSubscriptionIDs: Set<String> = []

    public init() {}

    public func isExpanded(subscriptionID: String) -> Bool {
        !collapsedSubscriptionIDs.contains(subscriptionID)
    }

    public mutating func toggle(subscriptionID: String) {
        if collapsedSubscriptionIDs.contains(subscriptionID) {
            collapsedSubscriptionIDs.remove(subscriptionID)
        } else {
            collapsedSubscriptionIDs.insert(subscriptionID)
        }
    }
}

public enum NativeHomePresentation {
    public static func subscriptions(from profiles: [ServerProfile]) -> [NativeSubscriptionSection] {
        let clusterTitles = preferredClusterTitles(in: profiles)
        var sections: [NativeSubscriptionSection] = []
        var sectionIndexByID: [String: Int] = [:]

        for profile in profiles {
            let clusterID = normalizedClusterID(profile.clusterID)
            let sectionID = clusterID.map { "cluster:\($0)" } ?? "standalone"

            if let index = sectionIndexByID[sectionID] {
                sections[index].profiles.append(profile)
                continue
            }

            let title = clusterID.flatMap { clusterTitles[$0] } ?? "Серверы"
            sectionIndexByID[sectionID] = sections.endIndex
            sections.append(
                NativeSubscriptionSection(
                    id: sectionID,
                    title: title,
                    profiles: [profile]
                )
            )
        }

        return sections
    }

    public static func locationEmoji(for profile: ServerProfile) -> String {
        for location in [profile.region, profile.name] {
            if let flag = ServerLocationPresentation.flag(forRegion: location) {
                return flag
            }
        }

        let fallbackCountryCode = profile.serverIdentity == "neproto.lyntragram.ru"
            ? "RU"
            : countryCodeFromIdentity(profile.serverIdentity)
        return ServerLocationPresentation.flag(forRegion: nil, fallbackCountryCode: fallbackCountryCode) ?? "🌐"
    }

    public static func pingableProfiles(in subscription: NativeSubscriptionSection) -> [ServerProfile] {
        subscription.profiles.filter(\.clusterAvailable)
    }

    private static func preferredClusterTitles(in profiles: [ServerProfile]) -> [String: String] {
        var firstNames: [String: String] = [:]
        var bootstrapNames: [String: String] = [:]

        for profile in profiles {
            guard let clusterID = normalizedClusterID(profile.clusterID) else { continue }
            let name = normalizedName(profile.name)
            if firstNames[clusterID] == nil {
                firstNames[clusterID] = name
            }
            if !profile.managedByCluster, bootstrapNames[clusterID] == nil {
                bootstrapNames[clusterID] = name
            }
        }

        var titles = firstNames
        for (clusterID, name) in bootstrapNames {
            titles[clusterID] = name
        }
        return titles
    }

    private static func normalizedClusterID(_ value: String?) -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private static func normalizedName(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "Подписка" : trimmed
    }

    private static func countryCodeFromIdentity(_ identity: String) -> String? {
        guard let topLevelDomain = identity.split(separator: ".").last,
              topLevelDomain.utf8.count == 2 else {
            return nil
        }
        return String(topLevelDomain).uppercased()
    }
}
