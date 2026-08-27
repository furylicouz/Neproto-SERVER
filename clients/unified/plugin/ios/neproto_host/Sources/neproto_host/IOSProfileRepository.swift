import Foundation
import NeProtoCore

@MainActor
protocol IOSCredentialStore {
  func save(secret: String, profileID: UUID) throws -> Data
  func persistentReference(profileID: UUID) throws -> Data
  func read(profileID: UUID) throws -> String
  func delete(profileID: UUID) throws
}

extension KeychainSecretStore: IOSCredentialStore {}

enum IOSProfileRepositoryError: Error {
  case invalidProfileID
  case profileNotFound
  case selectedProfileMissing
}

/// Read-through adapter for the existing native iOS v1 profile store. It adds
/// only a selected-profile pointer; existing profile JSON and Keychain items
/// remain authoritative and are never rewritten during initialization.
@MainActor
final class IOSProfileRepository {
  static let profileStorageKey = "np2.server-profiles.v1"
  static let selectedProfileKey = "np2.selected-profile-id.v1"

  private let defaults: UserDefaults
  private let credentials: any IOSCredentialStore
  private(set) var profiles: [ServerProfile]
  private(set) var selectedProfileID: UUID?

  init(
    defaults: UserDefaults = .standard,
    credentials: any IOSCredentialStore = KeychainSecretStore()
  ) {
    self.defaults = defaults
    self.credentials = credentials
    profiles = Self.loadProfiles(defaults: defaults)
    if let raw = defaults.string(forKey: Self.selectedProfileKey),
       let selected = UUID(uuidString: raw),
       profiles.contains(where: { $0.id == selected }) {
      selectedProfileID = selected
    } else {
      selectedProfileID = profiles.first?.id
    }
  }

  func summaries() -> [ProfileSummary] {
    profiles.map(summary)
  }

  func profile(id rawID: String) throws -> ServerProfile {
    guard let id = UUID(uuidString: rawID) else {
      throw IOSProfileRepositoryError.invalidProfileID
    }
    guard let profile = profiles.first(where: { $0.id == id }) else {
      throw IOSProfileRepositoryError.profileNotFound
    }
    return profile
  }

  func selectedProfile() throws -> ServerProfile {
    guard let id = selectedProfileID,
          let profile = profiles.first(where: { $0.id == id }) else {
      throw IOSProfileRepositoryError.selectedProfileMissing
    }
    return profile
  }

  func persistentCredentialReference(profileID: UUID) throws -> Data {
    try credentials.persistentReference(profileID: profileID)
  }

  func importOnboarding(_ value: String) throws -> ProfileSummary {
    let onboarding = try OnboardingProfile(uri: value.trimmingCharacters(in: .whitespacesAndNewlines))
    let resolution = try OnboardingImportResolver.resolve(existing: profiles, onboarding: onboarding)
    let profile = resolution.profile
    let previousProfiles = profiles
    let previousSelected = selectedProfileID
    let previousSecret = try resolution.replacedProfileID.map { try credentials.read(profileID: $0) }

    do {
      _ = try credentials.save(secret: onboarding.secret, profileID: profile.id)
      if let replaced = resolution.replacedProfileID,
         let index = profiles.firstIndex(where: { $0.id == replaced }) {
        profiles[index] = profile
      } else {
        profiles.append(profile)
      }
      sortProfiles()
      if selectedProfileID == nil {
        selectedProfileID = profile.id
      }
      try persist()
    } catch {
      profiles = previousProfiles
      selectedProfileID = previousSelected
      if let previousSecret {
        try? credentials.save(secret: previousSecret, profileID: profile.id)
      } else {
        try? credentials.delete(profileID: profile.id)
      }
      throw error
    }
    return summary(profile)
  }

  func select(rawID: String) throws -> ProfileSummary {
    let profile = try profile(id: rawID)
    selectedProfileID = profile.id
    defaults.set(profile.id.uuidString.lowercased(), forKey: Self.selectedProfileKey)
    return summary(profile)
  }

  func remove(rawID: String) throws {
    let profile = try profile(id: rawID)
    do {
      try credentials.delete(profileID: profile.id)
    } catch KeychainSecretStoreError.itemNotFound {
      // Missing Keychain material must not make a redacted profile immortal.
    }
    profiles.removeAll { $0.id == profile.id }
    if selectedProfileID == profile.id {
      selectedProfileID = profiles.first?.id
    }
    try persist()
  }

  func strictProviderConfiguration(profile: ServerProfile) throws -> [String: Any] {
    let deviceID = InstallationIdentityStore(defaults: defaults).identifier()
    return [
      "profile_id": profile.id.uuidString.lowercased(),
      "device_id": deviceID.uuidString.lowercased(),
      "profile_payload": try profile.providerPayload(),
      "client_configuration": try profile.strictHTTPSClientConfigurationJSON(deviceID: deviceID),
      "client_routes": Data("[]".utf8),
      "carrier_policy": "https-only",
    ]
  }

  private func summary(_ profile: ServerProfile) -> ProfileSummary {
    let hasCredential = (try? credentials.persistentReference(profileID: profile.id)) != nil
    return ProfileSummary(
      id: profile.id.uuidString.lowercased(),
      displayName: profile.name,
      serverIdentity: profile.serverIdentity,
      host: profile.serverIdentity,
      selected: profile.id == selectedProfileID,
      hasCredential: hasCredential,
      origin: profile.managedByCluster ? .cluster : .imported,
      catalogManaged: profile.managedByCluster,
      updatedAtUnixMs: 0
    )
  }

  private func sortProfiles() {
    profiles.sort {
      if $0.managedByCluster != $1.managedByCluster {
        return $0.managedByCluster
      }
      return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
    }
  }

  private func persist() throws {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    defaults.set(try encoder.encode(profiles), forKey: Self.profileStorageKey)
    if let selectedProfileID {
      defaults.set(selectedProfileID.uuidString.lowercased(), forKey: Self.selectedProfileKey)
    } else {
      defaults.removeObject(forKey: Self.selectedProfileKey)
    }
  }

  private static func loadProfiles(defaults: UserDefaults) -> [ServerProfile] {
    guard let data = defaults.data(forKey: profileStorageKey),
          let profiles = try? JSONDecoder().decode([ServerProfile].self, from: data) else {
      return []
    }
    return profiles
  }
}
