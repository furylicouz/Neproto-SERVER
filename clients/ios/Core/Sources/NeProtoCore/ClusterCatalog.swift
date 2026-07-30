import CryptoKit
import Foundation
import Network

public enum ClusterCatalogError: Error, Equatable, LocalizedError {
    case malformedEnvelope
    case invalidSignature
    case clusterMismatch
    case userMismatch
    case revisionRollback
    case expired
    case invalidCatalog

    public var errorDescription: String? {
        switch self {
        case .malformedEnvelope: "Каталог серверов NP/2 повреждён."
        case .invalidSignature: "Подпись каталога серверов NP/2 недействительна."
        case .clusterMismatch: "Каталог относится к другому кластеру NP/2."
        case .userMismatch: "Каталог NP/2 выпущен для другого пользователя."
        case .revisionRollback: "Сервер прислал устаревшую ревизию каталога NP/2."
        case .expired: "Каталог серверов NP/2 истёк или ещё не действует."
        case .invalidCatalog: "Параметры каталога серверов NP/2 некорректны."
        }
    }
}

public struct ClusterCatalog: Codable, Equatable, Sendable {
    public let version: Int
    public let clusterID: String
    public let revision: UInt64
    public let issuedAt: String
    public let expiresAt: String
    public let userID: String
    public let servers: [ClusterServer]
    public let adminRoutes: [ClusterRoute]
    public let permissions: ClusterPermissions

    public init(version: Int, clusterID: String, revision: UInt64, issuedAt: String, expiresAt: String, userID: String, servers: [ClusterServer], adminRoutes: [ClusterRoute], permissions: ClusterPermissions) {
        self.version = version
        self.clusterID = clusterID
        self.revision = revision
        self.issuedAt = issuedAt
        self.expiresAt = expiresAt
        self.userID = userID
        self.servers = servers
        self.adminRoutes = adminRoutes
        self.permissions = permissions
    }

    enum CodingKeys: String, CodingKey {
        case version
        case clusterID = "cluster_id"
        case revision
        case issuedAt = "issued_at"
        case expiresAt = "expires_at"
        case userID = "user_id"
        case servers
        case adminRoutes = "admin_routes"
        case permissions
    }
}

public struct ClusterServer: Codable, Equatable, Identifiable, Sendable {
    public var id: String { nodeID }
    public let nodeID: String
    public let name: String
    public let region: String
    public let serverIdentity: String
    public let serverAddresses: [String]
    public let httpsPath: String?
    public let webRTCPath: String?
    public let http3Path: String?
    public let requireDatagrams: Bool
    public let enabled: Bool

    public init(nodeID: String, name: String, region: String, serverIdentity: String, serverAddresses: [String], httpsPath: String?, webRTCPath: String?, http3Path: String?, requireDatagrams: Bool, enabled: Bool) {
        self.nodeID = nodeID
        self.name = name
        self.region = region
        self.serverIdentity = serverIdentity
        self.serverAddresses = serverAddresses
        self.httpsPath = httpsPath
        self.webRTCPath = webRTCPath
        self.http3Path = http3Path
        self.requireDatagrams = requireDatagrams
        self.enabled = enabled
    }

    enum CodingKeys: String, CodingKey {
        case nodeID = "node_id"
        case name, region
        case serverIdentity = "server_identity"
        case serverAddresses = "server_addresses"
        case httpsPath = "https_path"
        case webRTCPath = "webrtc_path"
        case http3Path = "http3_path"
        case requireDatagrams = "require_datagrams"
        case enabled
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        nodeID = try container.decode(String.self, forKey: .nodeID)
        name = try container.decode(String.self, forKey: .name)
        region = try container.decode(String.self, forKey: .region)
        serverIdentity = try container.decode(String.self, forKey: .serverIdentity)
        serverAddresses = try container.decode([String].self, forKey: .serverAddresses)
        httpsPath = try container.decodeIfPresent(String.self, forKey: .httpsPath)
        webRTCPath = try container.decodeIfPresent(String.self, forKey: .webRTCPath)
        http3Path = try container.decodeIfPresent(String.self, forKey: .http3Path)
        requireDatagrams = try container.decodeIfPresent(Bool.self, forKey: .requireDatagrams) ?? false
        enabled = try container.decode(Bool.self, forKey: .enabled)
    }
}

public struct ClusterPermissions: Codable, Equatable, Sendable {
    public let allowAutoSelection: Bool
    public let allowClientRoutes: Bool

    public init(allowAutoSelection: Bool, allowClientRoutes: Bool) {
        self.allowAutoSelection = allowAutoSelection
        self.allowClientRoutes = allowClientRoutes
    }

    enum CodingKeys: String, CodingKey {
        case allowAutoSelection = "allow_auto_selection"
        case allowClientRoutes = "allow_client_routes"
    }
}

public enum ClusterRouteSource: String, Codable, Equatable, Sendable {
    case admin
    case client
}

public enum ClusterRouteActionKind: String, Codable, Equatable, Sendable {
    case direct
    case current
    case node
    case chain
    case block
    case auto
}

public struct ClusterPortRange: Codable, Equatable, Sendable {
    public let from: UInt16
    public let to: UInt16

    public init(from: UInt16, to: UInt16) {
        self.from = from
        self.to = to
    }
}

public struct ClusterRouteMatch: Codable, Equatable, Sendable {
    public var domainSuffixes: [String]
    public var cidrs: [String]
    public var geoIPCountries: [String]
    public var geoSiteCategories: [String]
    public var portRanges: [ClusterPortRange]
    public var protocols: [String]

    enum CodingKeys: String, CodingKey {
        case domainSuffixes = "domain_suffixes"
        case cidrs
        case geoIPCountries = "geoip_countries"
        case geoSiteCategories = "geosite_categories"
        case portRanges = "port_ranges"
        case protocols
    }

    public init(
        domainSuffixes: [String] = [],
        cidrs: [String] = [],
        geoIPCountries: [String] = [],
        geoSiteCategories: [String] = [],
        portRanges: [ClusterPortRange] = [],
        protocols: [String] = []
    ) {
        self.domainSuffixes = domainSuffixes
        self.cidrs = cidrs
        self.geoIPCountries = geoIPCountries
        self.geoSiteCategories = geoSiteCategories
        self.portRanges = portRanges
        self.protocols = protocols
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        domainSuffixes = try container.decodeIfPresent([String].self, forKey: .domainSuffixes) ?? []
        cidrs = try container.decodeIfPresent([String].self, forKey: .cidrs) ?? []
        geoIPCountries = try container.decodeIfPresent([String].self, forKey: .geoIPCountries) ?? []
        geoSiteCategories = try container.decodeIfPresent([String].self, forKey: .geoSiteCategories) ?? []
        portRanges = try container.decodeIfPresent([ClusterPortRange].self, forKey: .portRanges) ?? []
        protocols = try container.decodeIfPresent([String].self, forKey: .protocols) ?? []
    }
}

public struct ClusterRouteAction: Codable, Equatable, Sendable {
    public var kind: ClusterRouteActionKind
    public var nodeIDs: [String]

    enum CodingKeys: String, CodingKey {
        case kind
        case nodeIDs = "node_ids"
    }

    public init(kind: ClusterRouteActionKind, nodeIDs: [String] = []) {
        self.kind = kind
        self.nodeIDs = nodeIDs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        kind = try container.decode(ClusterRouteActionKind.self, forKey: .kind)
        nodeIDs = try container.decodeIfPresent([String].self, forKey: .nodeIDs) ?? []
    }
}

public struct ClusterRoute: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var priority: Int
    public var enabled: Bool
    public var source: ClusterRouteSource
    public var mandatory: Bool
    public var match: ClusterRouteMatch
    public var action: ClusterRouteAction

    public init(id: String, name: String, priority: Int, enabled: Bool, source: ClusterRouteSource, mandatory: Bool, match: ClusterRouteMatch, action: ClusterRouteAction) {
        self.id = id
        self.name = name
        self.priority = priority
        self.enabled = enabled
        self.source = source
        self.mandatory = mandatory
        self.match = match
        self.action = action
    }

    enum CodingKeys: String, CodingKey {
        case id, name, priority, enabled, source, mandatory, match, action
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        name = try container.decode(String.self, forKey: .name)
        priority = try container.decode(Int.self, forKey: .priority)
        enabled = try container.decode(Bool.self, forKey: .enabled)
        source = try container.decode(ClusterRouteSource.self, forKey: .source)
        mandatory = try container.decodeIfPresent(Bool.self, forKey: .mandatory) ?? false
        match = try container.decode(ClusterRouteMatch.self, forKey: .match)
        action = try container.decode(ClusterRouteAction.self, forKey: .action)
    }
}

public enum ClusterRoutePolicy {
    public static func effective(admin: [ClusterRoute], local: [ClusterRoute], allowClientRoutes: Bool) -> [ClusterRoute] {
        var routes = admin.filter { $0.enabled && $0.source == .admin }
        if allowClientRoutes {
            routes.append(contentsOf: local.filter { $0.enabled && $0.source == .client && !$0.mandatory })
        }
        return routes.sorted {
            if $0.mandatory != $1.mandatory { return $0.mandatory }
            if $0.priority != $1.priority { return $0.priority < $1.priority }
            return $0.id < $1.id
        }
    }
}

public enum ClusterRouteValidationError: Error, Equatable, LocalizedError {
    case invalidRoute

    public var errorDescription: String? { "Некорректный маршрут NP/2." }
}

public enum ClusterRouteValidator {
    public static func validateLocal(_ route: ClusterRoute, allowedNodeIDs: Set<String>) throws {
        guard route.source == .client, !route.mandatory,
              validIdentifier(route.id, maximum: 64),
              !route.name.isEmpty, route.name.count <= 96,
              route.name.trimmingCharacters(in: .whitespacesAndNewlines) == route.name,
              route.priority >= 0, route.priority <= 1_000_000,
              route.match.domainSuffixes.count <= 64,
              route.match.cidrs.count <= 64,
              route.match.portRanges.count <= 32,
              route.match.protocols.count <= 2,
              route.match.domainSuffixes.allSatisfy(validDomain),
              route.match.cidrs.allSatisfy(validCIDR),
              route.match.portRanges.allSatisfy({ $0.from > 0 && $0.to >= $0.from }),
              route.match.protocols.allSatisfy({ $0 == "tcp" || $0 == "udp" }) else {
            throw ClusterRouteValidationError.invalidRoute
        }
        switch route.action.kind {
        case .node:
            guard route.action.nodeIDs.count == 1,
                  allowedNodeIDs.contains(route.action.nodeIDs[0]) else {
                throw ClusterRouteValidationError.invalidRoute
            }
        case .chain:
            guard (2...3).contains(route.action.nodeIDs.count),
                  Set(route.action.nodeIDs).count == route.action.nodeIDs.count,
                  route.action.nodeIDs.allSatisfy(allowedNodeIDs.contains) else {
                throw ClusterRouteValidationError.invalidRoute
            }
        case .direct, .current, .block, .auto:
            guard route.action.nodeIDs.isEmpty else { throw ClusterRouteValidationError.invalidRoute }
        }
    }

    private static func validIdentifier(_ value: String, maximum: Int) -> Bool {
        !value.isEmpty && value.count <= maximum &&
            value.range(of: "^[a-z0-9][a-z0-9_-]*$", options: .regularExpression) != nil
    }

    private static func validDomain(_ value: String) -> Bool {
        guard value == value.lowercased(), value.count <= 253, !value.hasSuffix(".") else { return false }
        return value.split(separator: ".", omittingEmptySubsequences: false).allSatisfy { label in
            !label.isEmpty && label.count <= 63 && label.first != "-" && label.last != "-" &&
                label.utf8.allSatisfy { byte in
                    (byte >= 97 && byte <= 122) || (byte >= 48 && byte <= 57) || byte == 45
                }
        }
    }

    private static func validCIDR(_ value: String) -> Bool {
        let components = value.split(separator: "/", omittingEmptySubsequences: false)
        guard components.count == 2, let prefix = Int(components[1]) else { return false }
        let address = String(components[0])
        if IPv4Address(address) != nil { return (0...32).contains(prefix) }
        if IPv6Address(address) != nil { return (0...128).contains(prefix) }
        return false
    }
}

public enum ClusterCatalogVerifier {
    private static let maximumBytes = 256 * 1_024
    private static let maximumLifetime: TimeInterval = 24 * 60 * 60

    public static func verify(
        envelopeData: Data,
        pinnedPublicKey: String,
        expectedClusterID: String,
        expectedUserID: String? = nil,
        minimumRevision: UInt64,
        now: Date = .now
    ) throws -> ClusterCatalog {
        guard !envelopeData.isEmpty, envelopeData.count <= maximumBytes,
              let envelopeObject = try? JSONSerialization.jsonObject(with: envelopeData) as? [String: Any],
              Set(envelopeObject.keys) == Set(["version", "payload", "signature"]),
              envelopeObject["version"] as? Int == 1,
              let payloadEncoded = envelopeObject["payload"] as? String,
              let signatureEncoded = envelopeObject["signature"] as? String,
              let payload = Data(base64URL: payloadEncoded), payload.count <= maximumBytes,
              let signature = Data(base64URL: signatureEncoded), signature.count == 64,
              let publicKeyData = Data(base64URL: pinnedPublicKey), publicKeyData.count == 32 else {
            throw ClusterCatalogError.malformedEnvelope
        }
        let publicKey: Curve25519.Signing.PublicKey
        do {
            publicKey = try Curve25519.Signing.PublicKey(rawRepresentation: publicKeyData)
        } catch {
            throw ClusterCatalogError.malformedEnvelope
        }
        guard publicKey.isValidSignature(signature, for: payload) else {
            throw ClusterCatalogError.invalidSignature
        }
        guard let payloadObject = try? JSONSerialization.jsonObject(with: payload) as? [String: Any],
              Set(payloadObject.keys) == Set(["version", "cluster_id", "revision", "issued_at", "expires_at", "user_id", "servers", "admin_routes", "permissions"]) else {
            throw ClusterCatalogError.invalidCatalog
        }
        let catalog: ClusterCatalog
        do {
            catalog = try JSONDecoder().decode(ClusterCatalog.self, from: payload)
        } catch {
            throw ClusterCatalogError.invalidCatalog
        }
        guard catalog.version == 1, catalog.servers.count >= 1, catalog.servers.count <= 32,
              catalog.adminRoutes.count <= 512, !catalog.userID.isEmpty else {
            throw ClusterCatalogError.invalidCatalog
        }
        guard catalog.clusterID == expectedClusterID else { throw ClusterCatalogError.clusterMismatch }
        if let expectedUserID, catalog.userID != expectedUserID { throw ClusterCatalogError.userMismatch }
        guard catalog.revision >= minimumRevision else { throw ClusterCatalogError.revisionRollback }
        guard let issuedAt = parseDate(catalog.issuedAt), let expiresAt = parseDate(catalog.expiresAt),
              expiresAt > issuedAt, expiresAt.timeIntervalSince(issuedAt) <= maximumLifetime,
              now >= issuedAt.addingTimeInterval(-300), now < expiresAt else {
            throw ClusterCatalogError.expired
        }
        guard catalog.servers.allSatisfy(validServer), catalog.adminRoutes.allSatisfy(validAdminRoute) else {
            throw ClusterCatalogError.invalidCatalog
        }
        return catalog
    }

    private static func parseDate(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let parsed = formatter.date(from: value) { return parsed }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: value)
    }

    private static func validServer(_ server: ClusterServer) -> Bool {
        !server.nodeID.isEmpty && server.nodeID.count <= 64 && !server.name.isEmpty && server.name.count <= 96 &&
            !server.region.isEmpty && server.region.count <= 96 && !server.serverIdentity.isEmpty &&
            !server.serverAddresses.isEmpty && server.serverAddresses.count <= 8
    }

    private static func validAdminRoute(_ route: ClusterRoute) -> Bool {
        guard route.source == .admin, !route.id.isEmpty, route.id.count <= 64,
              route.priority >= 0, route.priority <= 1_000_000 else { return false }
        switch route.action.kind {
        case .node: return route.action.nodeIDs.count == 1
        case .chain: return (2...3).contains(route.action.nodeIDs.count) && Set(route.action.nodeIDs).count == route.action.nodeIDs.count
        case .direct, .current, .block, .auto: return route.action.nodeIDs.isEmpty
        }
    }
}

private extension Data {
    init?(base64URL: String) {
        let remainder = base64URL.count % 4
        guard remainder != 1, base64URL.range(of: "^[A-Za-z0-9_-]+$", options: .regularExpression) != nil else { return nil }
        let padding = remainder == 0 ? "" : String(repeating: "=", count: 4 - remainder)
        self.init(base64Encoded: base64URL.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + padding)
    }
}
