import Foundation

public enum OnboardingImportResolutionError: Error, Equatable, LocalizedError {
    case duplicateCredential
    case clusterIdentityMismatch

    public var errorDescription: String? {
        switch self {
        case .duplicateCredential:
            "Этот ключ NP/2 уже добавлен на устройство. Для обновления требуется QR с привязкой к кластеру."
        case .clusterIdentityMismatch:
            "Новый QR относится к другому кластеру NP/2 или использует другой ключ каталога."
        }
    }
}

public struct OnboardingImportResolution: Equatable, Sendable {
    public let profile: ServerProfile
    public let replacedProfileID: UUID?

    public init(profile: ServerProfile, replacedProfileID: UUID?) {
        self.profile = profile
        self.replacedProfileID = replacedProfileID
    }
}

public enum OnboardingImportResolver {
    public static func resolve(
        existing: [ServerProfile],
        onboarding: OnboardingProfile
    ) throws -> OnboardingImportResolution {
        var incoming = try onboarding.serverProfile()
        let duplicates = existing.filter { $0.credentialID == onboarding.credentialID }
        guard !duplicates.isEmpty else {
            return OnboardingImportResolution(profile: incoming, replacedProfileID: nil)
        }

        let sameServer = duplicates.filter { $0.serverIdentity == onboarding.serverIdentity }
        let candidate: ServerProfile?
        if sameServer.count == 1 {
            candidate = sameServer[0]
        } else if sameServer.isEmpty, duplicates.count == 1 {
            candidate = duplicates[0]
        } else {
            candidate = nil
        }
        guard let candidate,
              let incomingClusterID = incoming.clusterID,
              let incomingCatalogKey = incoming.catalogPublicKey else {
            throw OnboardingImportResolutionError.duplicateCredential
        }
        if let existingClusterID = candidate.clusterID,
           existingClusterID != incomingClusterID || candidate.catalogPublicKey != incomingCatalogKey {
            throw OnboardingImportResolutionError.clusterIdentityMismatch
        }

        incoming.id = candidate.id
        return OnboardingImportResolution(profile: incoming, replacedProfileID: candidate.id)
    }
}
