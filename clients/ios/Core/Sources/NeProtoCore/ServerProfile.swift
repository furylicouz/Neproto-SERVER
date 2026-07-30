import Foundation
import Network

public enum CoverProfile: String, Codable, CaseIterable, Identifiable, Sendable {
    case quiet
    case web
    case interactive

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .quiet: "Тихий"
        case .web: "Веб"
        case .interactive: "Интерактивный"
        }
    }
}

public enum ProfileValidationError: Error, Equatable, LocalizedError {
    case invalidName
    case invalidServerIdentity
    case invalidServerAddress
    case invalidHTTPSPath
    case invalidWebRTCPath
    case invalidHTTP3Path
    case duplicatePaths
    case invalidCarrierPool
    case invalidSecret
    case invalidClusterPin

    public var errorDescription: String? {
        switch self {
        case .invalidName: "Введите название сервера длиной от 1 до 64 символов."
        case .invalidServerIdentity: "Домен должен быть в нижнем регистре и содержать только допустимые DNS-символы."
        case .invalidServerAddress: "Укажите публичный IPv4 или IPv6-адрес NP/2-сервера."
        case .invalidHTTPSPath: "HTTPS-путь должен быть приватным абсолютным путём длиной не менее 16 символов."
        case .invalidWebRTCPath: "WebRTC-путь должен быть приватным абсолютным путём длиной не менее 16 символов."
        case .invalidHTTP3Path: "HTTP/3-путь должен быть приватным абсолютным путём длиной не менее 16 символов."
        case .duplicatePaths: "HTTPS, WebRTC и HTTP/3 должны использовать разные пути."
        case .invalidCarrierPool: "Количество carrier-соединений должно быть от 1 до 3."
        case .invalidSecret: "Ключ должен быть каноническим 256-битным base64url без символа =."
        case .invalidClusterPin: "Ключ каталога кластера NP/2 некорректен."
        }
    }
}

public struct ServerProfile: Codable, Identifiable, Equatable, Sendable {
    public var id: UUID
    public var credentialID: String?
    public var name: String
    public var serverIdentity: String
    public var serverAddress: String?
    public var httpsPath: String
    public var webRTCPath: String
    public var http3Path: String?
    public var requireDatagrams: Bool
    public var maxParallelCarriers: Int
    public var enableConstellation: Bool
    public var enableForwardSecrecy: Bool
    public var clusterID: String?
    public var catalogPublicKey: String?
    public var clusterNodeID: String?
    public var managedByCluster: Bool
    public var region: String?
    public var clusterAvailable: Bool
    public var coverProfile: CoverProfile

    public init(
        id: UUID = UUID(),
        credentialID: String? = nil,
        name: String,
        serverIdentity: String,
        serverAddress: String? = nil,
        httpsPath: String,
        webRTCPath: String,
        http3Path: String? = nil,
        requireDatagrams: Bool = false,
        maxParallelCarriers: Int = 3,
        enableConstellation: Bool = true,
        enableForwardSecrecy: Bool = true,
        clusterID: String? = nil,
        catalogPublicKey: String? = nil,
        clusterNodeID: String? = nil,
        managedByCluster: Bool = false,
        region: String? = nil,
        clusterAvailable: Bool = true,
        coverProfile: CoverProfile
    ) {
        self.id = id
        self.credentialID = credentialID
        self.name = name
        self.serverIdentity = serverIdentity
        self.serverAddress = serverAddress
        self.httpsPath = httpsPath
        self.webRTCPath = webRTCPath
        self.http3Path = http3Path
        self.requireDatagrams = requireDatagrams
        self.maxParallelCarriers = maxParallelCarriers
        self.enableConstellation = enableConstellation
        self.enableForwardSecrecy = enableForwardSecrecy
        self.clusterID = clusterID
        self.catalogPublicKey = catalogPublicKey
        self.clusterNodeID = clusterNodeID
        self.managedByCluster = managedByCluster
        self.region = region
        self.clusterAvailable = clusterAvailable
        self.coverProfile = coverProfile
    }

    public func validate(secret: String) throws {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty, trimmedName.count <= 64 else {
            throw ProfileValidationError.invalidName
        }
        guard Self.isValidIdentity(serverIdentity) else {
            throw ProfileValidationError.invalidServerIdentity
        }
        guard let address = effectiveServerAddresses.first, Self.isValidIPAddress(address) else {
            throw ProfileValidationError.invalidServerAddress
        }
        guard Self.isValidPrivatePath(httpsPath) else {
            throw ProfileValidationError.invalidHTTPSPath
        }
        guard Self.isValidPrivatePath(webRTCPath) else {
            throw ProfileValidationError.invalidWebRTCPath
        }
        let normalizedHTTP3Path = effectiveHTTP3Path
        if let normalizedHTTP3Path, !normalizedHTTP3Path.isEmpty,
           !Self.isValidPrivatePath(normalizedHTTP3Path) {
            throw ProfileValidationError.invalidHTTP3Path
        }
        let paths = [httpsPath, webRTCPath] + (normalizedHTTP3Path.map { [$0] } ?? [])
        guard Set(paths).count == paths.count else {
            throw ProfileValidationError.duplicatePaths
        }
        guard (1...3).contains(maxParallelCarriers) else {
            throw ProfileValidationError.invalidCarrierPool
        }
        guard Self.decodeSecret(secret) != nil else {
            throw ProfileValidationError.invalidSecret
        }
        if (clusterID == nil) != (catalogPublicKey == nil) ||
            (clusterID != nil && (!Self.isValidClusterID(clusterID!) || !Self.isValidCatalogKey(catalogPublicKey!))) {
            throw ProfileValidationError.invalidClusterPin
        }
    }

    public func clientConfigurationJSON() throws -> Data {
        let normalizedHTTP3Path = effectiveHTTP3Path
        let configuration = ClientConfiguration(
            serverIdentity: serverIdentity,
            serverAddresses: effectiveServerAddresses,
            secretFile: "keychain",
            httpsURL: "wss://\(serverIdentity)\(httpsPath)",
            webRTCSignalingURL: "https://\(serverIdentity)\(webRTCPath)",
            http3URL: normalizedHTTP3Path.map { "https://\(serverIdentity)\($0)" },
            profile: coverProfile.rawValue,
            carrierPolicy: "performance",
            maxCoverOverheadPercent: 30,
            initialWindowBytes: 2_097_152,
            maxStreams: 128,
            maxParallelCarriers: maxParallelCarriers,
            requireDatagrams: requireDatagrams,
            enableConstellation: enableConstellation,
            enableForwardSecrecy: enableForwardSecrecy,
            webRTCTimeout: "5s",
            httpsTimeout: "10s",
            http3Timeout: normalizedHTTP3Path == nil ? nil : "5s",
            carrierCacheTTL: "10m"
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return try encoder.encode(configuration)
    }

    public func providerPayload() throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return try encoder.encode(self)
    }

    public init(providerPayload: Data) throws {
        self = try JSONDecoder().decode(Self.self, from: providerPayload)
    }

    private enum CodingKeys: String, CodingKey {
        case id, credentialID, name, serverIdentity, serverAddress
        case httpsPath, webRTCPath, http3Path, requireDatagrams, maxParallelCarriers
        case enableConstellation, enableForwardSecrecy, coverProfile
        case clusterID, catalogPublicKey, clusterNodeID, managedByCluster, region, clusterAvailable
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UUID.self, forKey: .id)
        credentialID = try container.decodeIfPresent(String.self, forKey: .credentialID)
        name = try container.decode(String.self, forKey: .name)
        serverIdentity = try container.decode(String.self, forKey: .serverIdentity)
        serverAddress = try container.decodeIfPresent(String.self, forKey: .serverAddress)
        httpsPath = try container.decode(String.self, forKey: .httpsPath)
        webRTCPath = try container.decode(String.self, forKey: .webRTCPath)
        http3Path = try container.decodeIfPresent(String.self, forKey: .http3Path)
        requireDatagrams = try container.decodeIfPresent(Bool.self, forKey: .requireDatagrams) ?? false
        maxParallelCarriers = try container.decodeIfPresent(Int.self, forKey: .maxParallelCarriers) ?? 3
        enableConstellation = try container.decodeIfPresent(Bool.self, forKey: .enableConstellation) ?? false
        enableForwardSecrecy = try container.decodeIfPresent(Bool.self, forKey: .enableForwardSecrecy) ?? false
        clusterID = try container.decodeIfPresent(String.self, forKey: .clusterID)
        catalogPublicKey = try container.decodeIfPresent(String.self, forKey: .catalogPublicKey)
        clusterNodeID = try container.decodeIfPresent(String.self, forKey: .clusterNodeID)
        managedByCluster = try container.decodeIfPresent(Bool.self, forKey: .managedByCluster) ?? false
        region = try container.decodeIfPresent(String.self, forKey: .region)
        clusterAvailable = try container.decodeIfPresent(Bool.self, forKey: .clusterAvailable) ?? true
        coverProfile = try container.decode(CoverProfile.self, forKey: .coverProfile)
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encodeIfPresent(credentialID, forKey: .credentialID)
        try container.encode(name, forKey: .name)
        try container.encode(serverIdentity, forKey: .serverIdentity)
        try container.encodeIfPresent(serverAddress, forKey: .serverAddress)
        try container.encode(httpsPath, forKey: .httpsPath)
        try container.encode(webRTCPath, forKey: .webRTCPath)
        try container.encodeIfPresent(http3Path, forKey: .http3Path)
        try container.encode(requireDatagrams, forKey: .requireDatagrams)
        try container.encode(maxParallelCarriers, forKey: .maxParallelCarriers)
        try container.encode(enableConstellation, forKey: .enableConstellation)
        try container.encode(enableForwardSecrecy, forKey: .enableForwardSecrecy)
        try container.encodeIfPresent(clusterID, forKey: .clusterID)
        try container.encodeIfPresent(catalogPublicKey, forKey: .catalogPublicKey)
        try container.encodeIfPresent(clusterNodeID, forKey: .clusterNodeID)
        try container.encode(managedByCluster, forKey: .managedByCluster)
        try container.encodeIfPresent(region, forKey: .region)
        try container.encode(clusterAvailable, forKey: .clusterAvailable)
        try container.encode(coverProfile, forKey: .coverProfile)
    }

    private static func isValidIdentity(_ value: String) -> Bool {
        guard !value.isEmpty, value.count <= 253, value == value.lowercased(), !value.hasSuffix(".") else {
            return false
        }
        return value.split(separator: ".", omittingEmptySubsequences: false).allSatisfy { label in
            guard !label.isEmpty, label.count <= 63, label.first != "-", label.last != "-" else {
                return false
            }
            return label.utf8.allSatisfy { byte in
                (byte >= 97 && byte <= 122) || (byte >= 48 && byte <= 57) || byte == 45
            }
        }
    }

    private var effectiveServerAddresses: [String] {
        if let address = serverAddress?.trimmingCharacters(in: .whitespacesAndNewlines), !address.isEmpty {
            return [address]
        }
        return []
    }

    private var effectiveHTTP3Path: String? {
        guard let value = http3Path?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else {
            return nil
        }
        return value
    }

    private static func isValidIPAddress(_ value: String) -> Bool {
        if let address = IPv4Address(value) {
            let octets = [UInt8](address.rawValue)
            guard octets.count == 4 else { return false }
            let first = octets[0]
            let second = octets[1]
            if first == 0 || first == 10 || first == 127 || first >= 224 ||
                (first == 100 && (64...127).contains(second)) ||
                (first == 169 && second == 254) ||
                (first == 172 && (16...31).contains(second)) ||
                (first == 192 && (second == 0 || second == 168)) ||
                (first == 198 && (second == 18 || second == 19)) {
                return false
            }
            return !((first == 192 && second == 0 && octets[2] == 2) ||
                (first == 198 && second == 51 && octets[2] == 100) ||
                (first == 203 && second == 0 && octets[2] == 113))
        }
        guard let address = IPv6Address(value) else { return false }
        let bytes = [UInt8](address.rawValue)
        guard bytes.count == 16, bytes.contains(where: { $0 != 0 }) else { return false }
        let isLoopback = bytes.dropLast().allSatisfy { $0 == 0 } && bytes.last == 1
        let isUniqueLocal = bytes[0] & 0xFE == 0xFC
        let isLinkLocal = bytes[0] == 0xFE && bytes[1] & 0xC0 == 0x80
        let isDocumentation = bytes[0...3].elementsEqual([0x20, 0x01, 0x0D, 0xB8])
        return !isLoopback && !isUniqueLocal && !isLinkLocal && bytes[0] != 0xFF && !isDocumentation
    }

    private static func isValidPrivatePath(_ value: String) -> Bool {
        guard value.count >= 16, value.hasPrefix("/"), !value.contains("//"),
              !value.contains("%"), !value.contains("\\"),
              !value.contains("?"), !value.contains("#") else {
            return false
        }
        return value.dropFirst().split(separator: "/", omittingEmptySubsequences: false).allSatisfy {
            !$0.isEmpty && $0 != "." && $0 != ".."
        }
    }

    private static func decodeSecret(_ encoded: String) -> Data? {
        guard encoded.count == 43,
              encoded.range(of: "^[A-Za-z0-9_-]{43}$", options: .regularExpression) != nil else {
            return nil
        }
        let canonical = encoded
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/") + "="
        guard let decoded = Data(base64Encoded: canonical), decoded.count == 32,
              decoded.contains(where: { $0 != 0 }),
              decoded.base64URLEncodedString() == encoded else {
            return nil
        }
        return decoded
    }

    private static func isValidClusterID(_ value: String) -> Bool {
        value.count <= 64 && value.range(of: "^[a-z0-9][a-z0-9_-]*$", options: .regularExpression) != nil
    }

    private static func isValidCatalogKey(_ encoded: String) -> Bool {
        guard encoded.range(of: "^[A-Za-z0-9_-]{43}$", options: .regularExpression) != nil else { return false }
        let canonical = encoded
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/") + "="
        guard let decoded = Data(base64Encoded: canonical), decoded.count == 32,
              decoded.contains(where: { $0 != 0 }) else { return false }
        return decoded.base64URLEncodedString() == encoded
    }
}

private struct ClientConfiguration: Codable {
    let serverIdentity: String
    let serverAddresses: [String]
    let secretFile: String
    let httpsURL: String
    let webRTCSignalingURL: String
    let http3URL: String?
    let profile: String
    let carrierPolicy: String
    let maxCoverOverheadPercent: Int
    let initialWindowBytes: Int
    let maxStreams: Int
    let maxParallelCarriers: Int
    let requireDatagrams: Bool
    let enableConstellation: Bool
    let enableForwardSecrecy: Bool
    let webRTCTimeout: String
    let httpsTimeout: String
    let http3Timeout: String?
    let carrierCacheTTL: String

    enum CodingKeys: String, CodingKey {
        case serverIdentity = "server_identity"
        case serverAddresses = "server_addresses"
        case secretFile = "secret_file"
        case httpsURL = "https_url"
        case webRTCSignalingURL = "webrtc_signaling_url"
        case http3URL = "http3_url"
        case profile
        case carrierPolicy = "carrier_policy"
        case maxCoverOverheadPercent = "max_cover_overhead_percent"
        case initialWindowBytes = "initial_window_bytes"
        case maxStreams = "max_streams"
        case maxParallelCarriers = "max_parallel_carriers"
        case requireDatagrams = "require_datagrams"
        case enableConstellation = "enable_constellation"
        case enableForwardSecrecy = "enable_forward_secrecy"
        case webRTCTimeout = "webrtc_timeout"
        case httpsTimeout = "https_timeout"
        case http3Timeout = "http3_timeout"
        case carrierCacheTTL = "carrier_cache_ttl"
    }
}

private extension Data {
    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
