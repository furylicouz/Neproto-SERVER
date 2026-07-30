import Foundation

public enum OnboardingProfileError: Error, Equatable, LocalizedError {
    case malformedURI
    case malformedPayload
    case unsupportedVersion
    case invalidCredentialID
    case invalidProfile

    public var errorDescription: String? {
        switch self {
        case .malformedURI: "QR-код не является конфигурацией NP/2."
        case .malformedPayload: "Конфигурация NP/2 повреждена или содержит неизвестные поля."
        case .unsupportedVersion: "Эта версия конфигурации NP/2 пока не поддерживается."
        case .invalidCredentialID: "Идентификатор ключа NP/2 некорректен."
        case .invalidProfile: "Параметры NP/2 в QR-коде некорректны."
        }
    }
}

public struct OnboardingProfile: Codable, Equatable, Sendable {
    public static let legacyPrefix = "np2://import/v1/"
    public static let prefix = "np2://import/v2/"
    public static let maximumURIBytes = 4_096

    public let version: Int
    public let credentialID: String
    public let name: String
    public let serverIdentity: String
    public let serverAddresses: [String]
    public let httpsPath: String
    public let webRTCPath: String
    public let http3Path: String?
    public let requireDatagrams: Bool?
    public let maxParallelCarriers: Int?
    public let enableConstellation: Bool?
    public let enableForwardSecrecy: Bool?
    public let clusterID: String?
    public let catalogPublicKey: String?
    public let profile: String
    public let secret: String

    enum CodingKeys: String, CodingKey, CaseIterable {
        case version
        case credentialID = "credential_id"
        case name
        case serverIdentity = "server_identity"
        case serverAddresses = "server_addresses"
        case httpsPath = "https_path"
        case webRTCPath = "webrtc_path"
        case http3Path = "http3_path"
        case requireDatagrams = "require_datagrams"
        case maxParallelCarriers = "max_parallel_carriers"
        case enableConstellation = "enable_constellation"
        case enableForwardSecrecy = "enable_forward_secrecy"
        case clusterID = "cluster_id"
        case catalogPublicKey = "catalog_public_key"
        case profile
        case secret
    }

    public init(uri: String) throws {
        guard uri.utf8.count <= Self.maximumURIBytes else {
            throw OnboardingProfileError.malformedURI
        }
        let matchedPrefix: String
        let expectedVersion: Int
        if uri.hasPrefix(Self.prefix) {
            matchedPrefix = Self.prefix
            expectedVersion = 2
        } else if uri.hasPrefix(Self.legacyPrefix) {
            matchedPrefix = Self.legacyPrefix
            expectedVersion = 1
        } else {
            throw OnboardingProfileError.malformedURI
        }
        let encoded = String(uri.dropFirst(matchedPrefix.count))
        guard !encoded.isEmpty,
              encoded.range(of: "^[A-Za-z0-9_-]+$", options: .regularExpression) != nil,
              let data = Data(base64URL: encoded),
              data.count <= Self.maximumURIBytes else {
            throw OnboardingProfileError.malformedPayload
        }
        try Self.validateJSONShape(data)
        do {
            self = try JSONDecoder().decode(Self.self, from: data)
        } catch {
            throw OnboardingProfileError.malformedPayload
        }
        guard version == expectedVersion else { throw OnboardingProfileError.unsupportedVersion }
        guard let identifier = Data(base64URL: credentialID),
              identifier.count == 16,
              identifier.contains(where: { $0 != 0 }),
              identifier.base64URLEncodedString() == credentialID else {
            throw OnboardingProfileError.invalidCredentialID
        }
        guard let cover = CoverProfile(rawValue: profile),
              let address = serverAddresses.first,
              serverAddresses.count <= 8,
              Set(serverAddresses).count == serverAddresses.count else {
            throw OnboardingProfileError.invalidProfile
        }
        if version == 2 {
            guard let http3Path, !http3Path.isEmpty else {
                throw OnboardingProfileError.invalidProfile
            }
            if let maxParallelCarriers, !(1...3).contains(maxParallelCarriers) {
                throw OnboardingProfileError.invalidProfile
            }
        } else if http3Path != nil || requireDatagrams != nil || maxParallelCarriers != nil ||
                    enableConstellation != nil || enableForwardSecrecy != nil || clusterID != nil || catalogPublicKey != nil {
            throw OnboardingProfileError.invalidProfile
        }
        if (clusterID == nil) != (catalogPublicKey == nil) {
            throw OnboardingProfileError.invalidProfile
        }
        let candidate = ServerProfile(
            credentialID: credentialID,
            name: name,
            serverIdentity: serverIdentity,
            serverAddress: address,
            httpsPath: httpsPath,
            webRTCPath: webRTCPath,
            http3Path: http3Path,
            requireDatagrams: requireDatagrams ?? false,
            maxParallelCarriers: maxParallelCarriers ?? 3,
            enableConstellation: enableConstellation ?? false,
            enableForwardSecrecy: enableForwardSecrecy ?? false,
            clusterID: clusterID,
            catalogPublicKey: catalogPublicKey,
            coverProfile: cover
        )
        do {
            try candidate.validate(secret: secret)
        } catch {
            throw OnboardingProfileError.invalidProfile
        }
    }

    public func serverProfile() throws -> ServerProfile {
        guard let cover = CoverProfile(rawValue: profile),
              let address = serverAddresses.first else {
            throw OnboardingProfileError.invalidProfile
        }
        return ServerProfile(
            credentialID: credentialID,
            name: name,
            serverIdentity: serverIdentity,
            serverAddress: address,
            httpsPath: httpsPath,
            webRTCPath: webRTCPath,
            http3Path: http3Path,
            requireDatagrams: requireDatagrams ?? false,
            maxParallelCarriers: maxParallelCarriers ?? 3,
            enableConstellation: enableConstellation ?? false,
            enableForwardSecrecy: enableForwardSecrecy ?? false,
            clusterID: clusterID,
            catalogPublicKey: catalogPublicKey,
            coverProfile: cover
        )
    }

    private static func validateJSONShape(_ data: Data) throws {
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw OnboardingProfileError.malformedPayload
        }
        guard let version = object["version"] as? Int else {
            throw OnboardingProfileError.malformedPayload
        }
        var required = Set([
            CodingKeys.version.rawValue,
            CodingKeys.credentialID.rawValue,
            CodingKeys.name.rawValue,
            CodingKeys.serverIdentity.rawValue,
            CodingKeys.serverAddresses.rawValue,
            CodingKeys.httpsPath.rawValue,
            CodingKeys.webRTCPath.rawValue,
            CodingKeys.profile.rawValue,
            CodingKeys.secret.rawValue,
        ])
        var allowed = required
        if version == 2 {
            required.insert(CodingKeys.http3Path.rawValue)
            allowed.insert(CodingKeys.http3Path.rawValue)
            allowed.insert(CodingKeys.requireDatagrams.rawValue)
            allowed.insert(CodingKeys.maxParallelCarriers.rawValue)
            allowed.insert(CodingKeys.enableConstellation.rawValue)
            allowed.insert(CodingKeys.enableForwardSecrecy.rawValue)
            allowed.insert(CodingKeys.clusterID.rawValue)
            allowed.insert(CodingKeys.catalogPublicKey.rawValue)
        }
        let actual = Set(object.keys)
        guard required.isSubset(of: actual), actual.isSubset(of: allowed) else {
            throw OnboardingProfileError.malformedPayload
        }
        let raw = String(decoding: data, as: UTF8.self)
        let expression = try NSRegularExpression(pattern: #"\"([^\"\\]+)\"\s*:"#)
        let matches = expression.matches(in: raw, range: NSRange(raw.startIndex..., in: raw))
        let keys = matches.compactMap { match -> String? in
            guard let range = Range(match.range(at: 1), in: raw) else { return nil }
            return String(raw[range])
        }
        guard keys.count == actual.count, Set(keys) == actual else {
            throw OnboardingProfileError.malformedPayload
        }
    }
}

private extension Data {
    init?(base64URL: String) {
        let remainder = base64URL.count % 4
        guard remainder != 1 else { return nil }
        let padding = remainder == 0 ? "" : String(repeating: "=", count: 4 - remainder)
        self.init(
            base64Encoded: base64URL
                .replacingOccurrences(of: "-", with: "+")
                .replacingOccurrences(of: "_", with: "/") + padding
        )
    }

    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
