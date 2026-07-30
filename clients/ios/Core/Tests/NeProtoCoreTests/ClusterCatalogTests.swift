import CryptoKit
import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 signed cluster catalog")
struct ClusterCatalogTests {
    @Test("exact envelope payload is verified and decoded")
    func verifiesEnvelope() throws {
        let now = Date(timeIntervalSince1970: 1_774_099_200)
        let fixture = try makeFixture(now: now, revision: 7)
        let catalog = try ClusterCatalogVerifier.verify(
            envelopeData: fixture.envelope,
            pinnedPublicKey: fixture.publicKey,
            expectedClusterID: "cluster-01",
            expectedUserID: "alice",
            minimumRevision: 6,
            now: now.addingTimeInterval(60)
        )
        #expect(catalog.revision == 7)
        #expect(catalog.servers.map(\.nodeID) == ["master", "edge"])
        #expect(catalog.adminRoutes.map(\.id) == ["media"])
        #expect(catalog.adminRoutes[0].match.geoSiteCategories == ["openai"])
        #expect(catalog.adminRoutes[0].match.geoIPCountries == ["nl"])
        #expect(catalog.permissions.allowClientRoutes)
    }

    @Test("tampering rollback and expiry fail closed")
    func rejectsUnsafeCatalogs() throws {
        let now = Date(timeIntervalSince1970: 1_774_099_200)
        let fixture = try makeFixture(now: now, revision: 7)
        var object = try #require(JSONSerialization.jsonObject(with: fixture.envelope) as? [String: Any])
        object["payload"] = String((object["payload"] as! String).dropLast()) + "A"
        let tampered = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        #expect(throws: ClusterCatalogError.self) {
            try ClusterCatalogVerifier.verify(envelopeData: tampered, pinnedPublicKey: fixture.publicKey, expectedClusterID: "cluster-01", minimumRevision: 6, now: now)
        }
        #expect(throws: ClusterCatalogError.self) {
            try ClusterCatalogVerifier.verify(envelopeData: fixture.envelope, pinnedPublicKey: fixture.publicKey, expectedClusterID: "cluster-01", minimumRevision: 8, now: now)
        }
        #expect(throws: ClusterCatalogError.self) {
            try ClusterCatalogVerifier.verify(envelopeData: fixture.envelope, pinnedPublicKey: fixture.publicKey, expectedClusterID: "cluster-01", minimumRevision: 6, now: now.addingTimeInterval(7_201))
        }
        #expect(throws: ClusterCatalogError.userMismatch) {
            try ClusterCatalogVerifier.verify(envelopeData: fixture.envelope, pinnedPublicKey: fixture.publicKey, expectedClusterID: "cluster-01", expectedUserID: "mallory", minimumRevision: 6, now: now)
        }
    }

    @Test("mandatory administrator route always precedes local routes")
    func mergesRoutesDeterministically() {
        let admin = [
            ClusterRoute(id: "optional", name: "Optional", priority: 50, enabled: true, source: .admin, mandatory: false, match: .init(), action: .init(kind: .current, nodeIDs: [])),
            ClusterRoute(id: "mandatory", name: "Mandatory", priority: 100, enabled: true, source: .admin, mandatory: true, match: .init(), action: .init(kind: .block, nodeIDs: [])),
        ]
        let local = [ClusterRoute(id: "local", name: "Local", priority: 1, enabled: true, source: .client, mandatory: false, match: .init(), action: .init(kind: .direct, nodeIDs: []))]
        #expect(ClusterRoutePolicy.effective(admin: admin, local: local, allowClientRoutes: true).map(\.id) == ["mandatory", "local", "optional"])
        #expect(ClusterRoutePolicy.effective(admin: admin, local: local, allowClientRoutes: false).map(\.id) == ["mandatory", "optional"])
    }

    @Test("local route validation binds node actions to allowed catalog nodes")
    func validatesLocalRoutes() throws {
        let valid = ClusterRoute(
            id: "local-media", name: "Media", priority: 10, enabled: true,
            source: .client, mandatory: false,
            match: .init(domainSuffixes: ["youtube.com"], cidrs: ["8.8.8.0/24"], portRanges: [.init(from: 443, to: 443)], protocols: ["tcp"]),
            action: .init(kind: .node, nodeIDs: ["edge"])
        )
        try ClusterRouteValidator.validateLocal(valid, allowedNodeIDs: ["edge"])
        #expect(throws: ClusterRouteValidationError.invalidRoute) {
            try ClusterRouteValidator.validateLocal(valid, allowedNodeIDs: ["master"])
        }
        var invalid = valid
        invalid.match.domainSuffixes = ["YouTube.com"]
        #expect(throws: ClusterRouteValidationError.invalidRoute) {
            try ClusterRouteValidator.validateLocal(invalid, allowedNodeIDs: ["edge"])
        }
    }

    private func makeFixture(now: Date, revision: UInt64) throws -> (envelope: Data, publicKey: String) {
        let privateKey = try Curve25519.Signing.PrivateKey(rawRepresentation: Data(repeating: 0x44, count: 32))
        let issued = ISO8601DateFormatter.string(from: now, timeZone: .gmt, formatOptions: [.withInternetDateTime])
        let expires = ISO8601DateFormatter.string(from: now.addingTimeInterval(7_200), timeZone: .gmt, formatOptions: [.withInternetDateTime])
        let payloadObject: [String: Any] = [
            "version": 1, "cluster_id": "cluster-01", "revision": revision,
            "issued_at": issued, "expires_at": expires, "user_id": "alice",
            "servers": [
                ["node_id": "master", "name": "Master", "region": "Moscow", "server_identity": "vpn.example.com", "server_addresses": ["8.8.8.8"], "enabled": true],
                ["node_id": "edge", "name": "Edge", "region": "Helsinki", "server_identity": "edge.example.com", "server_addresses": ["1.1.1.1"], "enabled": true],
            ],
            "admin_routes": [[
                "id": "media", "name": "Media", "priority": 10, "enabled": true,
                "source": "admin", "match": [
                    "domain_suffixes": ["np2-geodata-never-match.invalid"],
                    "geoip_countries": ["nl"],
                    "geosite_categories": ["openai"],
                    "protocols": ["tcp"],
                ],
                "action": ["kind": "node", "node_ids": ["edge"]],
            ]],
            "permissions": ["allow_auto_selection": true, "allow_client_routes": true],
        ]
        let payload = try JSONSerialization.data(withJSONObject: payloadObject, options: [.sortedKeys])
        let signature = try privateKey.signature(for: payload)
        let envelope: [String: Any] = [
            "version": 1,
            "payload": payload.base64URLEncodedString(),
            "signature": signature.base64URLEncodedString(),
        ]
        return (
            try JSONSerialization.data(withJSONObject: envelope, options: [.sortedKeys]),
            privateKey.publicKey.rawRepresentation.base64URLEncodedString()
        )
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
