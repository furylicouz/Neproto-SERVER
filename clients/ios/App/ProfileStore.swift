import Foundation
import NeProtoCore

struct ClusterClientState: Codable, Equatable {
    var revision: UInt64
    var adminRoutes: [ClusterRoute]
    var localRoutes: [ClusterRoute]
    var permissions: ClusterPermissions
    var synchronizedAt: Date
}

enum ProfileStoreError: LocalizedError {
    case managedProfile
    case profileIsNotClusterBootstrap
    case clusterNotSynchronized
    case clientRoutesDisabled
    case invalidLocalRoute

    var errorDescription: String? {
        switch self {
        case .managedProfile: "Сервер управляется администратором кластера NP/2."
        case .profileIsNotClusterBootstrap: "Профиль не содержит криптографическую привязку к кластеру NP/2."
        case .clusterNotSynchronized: "Каталог кластера NP/2 ещё не синхронизирован."
        case .clientRoutesDisabled: "Администратор запретил локальные маршруты."
        case .invalidLocalRoute: "Некорректный локальный маршрут."
        }
    }
}

@MainActor
final class ProfileStore: ObservableObject {
    @Published private(set) var profiles: [ServerProfile]
    @Published private(set) var clusterStates: [String: ClusterClientState]

    private let defaults: UserDefaults
    private let secretStore: KeychainSecretStore
    private let storageKey = "np2.server-profiles.v1"
    private let clusterStorageKey = "np2.cluster-client-state.v1"

    init(defaults: UserDefaults = .standard, secretStore: KeychainSecretStore = KeychainSecretStore()) {
        self.defaults = defaults
        self.secretStore = secretStore
        if let data = defaults.data(forKey: storageKey),
           let decoded = try? JSONDecoder().decode([ServerProfile].self, from: data) {
            profiles = decoded
        } else {
            profiles = []
        }
        if let data = defaults.data(forKey: clusterStorageKey),
           let decoded = try? JSONDecoder().decode([String: ClusterClientState].self, from: data) {
            clusterStates = decoded
        } else {
            clusterStates = [:]
        }
    }

    func save(profile: ServerProfile, secret: String) throws {
        try profile.validate(secret: secret)
        _ = try secretStore.save(secret: secret, profileID: profile.id)
        if let index = profiles.firstIndex(where: { $0.id == profile.id }) {
            profiles[index] = profile
        } else {
            profiles.append(profile)
        }
        sortProfiles()
        try persistProfiles()
    }

    func remove(profileID: UUID) throws {
        if let profile = profiles.first(where: { $0.id == profileID }), profile.managedByCluster {
            throw ProfileStoreError.managedProfile
        }
        do {
            try secretStore.delete(profileID: profileID)
        } catch KeychainSecretStoreError.itemNotFound {
            // A missing credential must not make an otherwise deletable profile immortal.
        }
        profiles.removeAll { $0.id == profileID }
        try persistProfiles()
    }

    @discardableResult
    func importOnboardingURI(_ uri: String) throws -> ServerProfile {
        let onboarding = try OnboardingProfile(uri: uri)
        let resolution = try OnboardingImportResolver.resolve(
            existing: profiles,
            onboarding: onboarding
        )
        let profile = resolution.profile
        let previous = profiles
        let previousSecret = try resolution.replacedProfileID.map { try secretStore.read(profileID: $0) }
        do {
            _ = try secretStore.save(secret: onboarding.secret, profileID: profile.id)
        } catch {
            if let previousSecret {
                try? secretStore.save(secret: previousSecret, profileID: profile.id)
            }
            throw error
        }
        if let replacedProfileID = resolution.replacedProfileID,
           let index = profiles.firstIndex(where: { $0.id == replacedProfileID }) {
            profiles[index] = profile
        } else {
            profiles.append(profile)
        }
        sortProfiles()
        do {
            try persistProfiles()
        } catch {
            profiles = previous
            if let previousSecret {
                try? secretStore.save(secret: previousSecret, profileID: profile.id)
            } else {
                try? secretStore.delete(profileID: profile.id)
            }
            throw error
        }
        return profile
    }

    @discardableResult
    func applyClusterCatalog(_ envelope: Data, bootstrapProfileID: UUID, now: Date = .now) throws -> ClusterCatalog {
        guard let bootstrap = profiles.first(where: { $0.id == bootstrapProfileID }),
              let clusterID = bootstrap.clusterID,
              let publicKey = bootstrap.catalogPublicKey,
              let credentialID = bootstrap.credentialID else {
            throw ProfileStoreError.profileIsNotClusterBootstrap
        }
        let previousState = clusterStates[clusterID]
        let catalog = try ClusterCatalogVerifier.verify(
            envelopeData: envelope,
            pinnedPublicKey: publicKey,
            expectedClusterID: clusterID,
            expectedUserID: credentialID,
            minimumRevision: previousState?.revision ?? 0,
            now: now
        )
        let synchronized = try ClusterProfileSynchronizer.synchronize(
            existing: profiles,
            bootstrapProfileID: bootstrapProfileID,
            catalog: catalog
        )
        let secret = try secretStore.read(profileID: bootstrapProfileID)
        for profile in synchronized where profile.clusterID == clusterID {
            try profile.validate(secret: secret)
        }

        let oldProfiles = profiles
        let oldStates = clusterStates
        let oldIDs = Set(oldProfiles.map(\.id))
        let synchronizedIDs = Set(synchronized.map(\.id))
        let newProfiles = synchronized.filter {
            $0.clusterID == clusterID && $0.id != bootstrapProfileID && !oldIDs.contains($0.id)
        }
        do {
            for profile in newProfiles {
                _ = try secretStore.save(secret: secret, profileID: profile.id)
            }
            profiles = synchronized
            clusterStates[clusterID] = ClusterClientState(
                revision: catalog.revision,
                adminRoutes: catalog.adminRoutes,
                localRoutes: previousState?.localRoutes ?? [],
                permissions: catalog.permissions,
                synchronizedAt: now
            )
            try persistProfiles()
            try persistClusterStates()
        } catch {
            profiles = oldProfiles
            clusterStates = oldStates
            for profile in newProfiles { try? secretStore.delete(profileID: profile.id) }
            throw error
        }

        let withdrawn = oldProfiles.filter {
            $0.managedByCluster && $0.clusterID == clusterID && !synchronizedIDs.contains($0.id)
        }
        for profile in withdrawn { try? secretStore.delete(profileID: profile.id) }
        return catalog
    }

    func effectiveRoutes(for profileID: UUID) -> [ClusterRoute] {
        guard let clusterID = profiles.first(where: { $0.id == profileID })?.clusterID,
              let state = clusterStates[clusterID] else { return [] }
        return ClusterRoutePolicy.effective(
            admin: state.adminRoutes,
            local: state.localRoutes,
            allowClientRoutes: state.permissions.allowClientRoutes
        )
    }

    func localRoutesAllowed(for profileID: UUID) -> Bool {
        guard let clusterID = profiles.first(where: { $0.id == profileID })?.clusterID else { return false }
        return clusterStates[clusterID]?.permissions.allowClientRoutes ?? false
    }

    func upsertLocalRoute(_ route: ClusterRoute, profileID: UUID) throws {
        guard route.source == .client, !route.mandatory else { throw ProfileStoreError.invalidLocalRoute }
        guard let clusterID = profiles.first(where: { $0.id == profileID })?.clusterID,
              var state = clusterStates[clusterID] else { throw ProfileStoreError.clusterNotSynchronized }
        guard state.permissions.allowClientRoutes else { throw ProfileStoreError.clientRoutesDisabled }
        let allowedNodeIDs = Set(profiles.compactMap { profile in
            profile.clusterID == clusterID && profile.clusterAvailable ? profile.clusterNodeID : nil
        })
        try ClusterRouteValidator.validateLocal(route, allowedNodeIDs: allowedNodeIDs)
        if let index = state.localRoutes.firstIndex(where: { $0.id == route.id }) {
            state.localRoutes[index] = route
        } else {
            state.localRoutes.append(route)
        }
        clusterStates[clusterID] = state
        try persistClusterStates()
    }

    func removeLocalRoute(routeID: String, profileID: UUID) throws {
        guard let clusterID = profiles.first(where: { $0.id == profileID })?.clusterID,
              var state = clusterStates[clusterID] else { throw ProfileStoreError.clusterNotSynchronized }
        guard state.permissions.allowClientRoutes else { throw ProfileStoreError.clientRoutesDisabled }
        state.localRoutes.removeAll { $0.id == routeID }
        clusterStates[clusterID] = state
        try persistClusterStates()
    }

    private func sortProfiles() {
        profiles.sort {
            if $0.managedByCluster != $1.managedByCluster { return $0.managedByCluster }
            return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    private func persistProfiles() throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        defaults.set(try encoder.encode(profiles), forKey: storageKey)
    }

    private func persistClusterStates() throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        defaults.set(try encoder.encode(clusterStates), forKey: clusterStorageKey)
    }
}
