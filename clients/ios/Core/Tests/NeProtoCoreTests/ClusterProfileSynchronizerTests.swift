import Foundation
import Testing
@testable import NeProtoCore

@Suite("NP/2 cluster profile synchronization")
struct ClusterProfileSynchronizerTests {
    @Test("catalog adds allowed servers and removes withdrawn managed servers")
    func synchronizesProfiles() throws {
        let bootstrapID = UUID(uuidString: "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA")!
        let childID = UUID(uuidString: "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB")!
        let bootstrap = ServerProfile(
            id: bootstrapID, credentialID: "credential", name: "Bootstrap",
            serverIdentity: "vpn.example.com", serverAddress: "8.8.8.8",
            httpsPath: "/private/https/session", webRTCPath: "/private/webrtc/offer",
            http3Path: "/private/http3/session", clusterID: "cluster-01",
            catalogPublicKey: Data(repeating: 0x44, count: 32).base64URLEncodedString(),
            coverProfile: .interactive
        )
        let withdrawn = ServerProfile(
            id: childID, credentialID: "credential", name: "Withdrawn",
            serverIdentity: "old.example.com", serverAddress: "9.9.9.9",
            httpsPath: "/private/https/session", webRTCPath: "/private/webrtc/offer",
            clusterID: "cluster-01", catalogPublicKey: bootstrap.catalogPublicKey,
            clusterNodeID: "old", managedByCluster: true, coverProfile: .interactive
        )
        let catalog = ClusterCatalog(
            version: 1, clusterID: "cluster-01", revision: 4,
            issuedAt: "2026-07-19T12:00:00Z", expiresAt: "2026-07-19T13:00:00Z", userID: "credential",
            servers: [
                ClusterServer(nodeID: "master", name: "Moscow", region: "Russia", serverIdentity: "vpn.example.com", serverAddresses: ["8.8.8.8"], httpsPath: nil, webRTCPath: nil, http3Path: nil, requireDatagrams: false, enabled: true),
                ClusterServer(nodeID: "edge", name: "Helsinki", region: "Finland", serverIdentity: "edge.example.com", serverAddresses: ["1.1.1.1"], httpsPath: "/edge/https/session", webRTCPath: "/edge/webrtc/offer", http3Path: "/edge/http3/session", requireDatagrams: true, enabled: false),
            ],
            adminRoutes: [], permissions: .init(allowAutoSelection: true, allowClientRoutes: true)
        )
        var generated = [UUID(uuidString: "CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC")!]
        let profiles = try ClusterProfileSynchronizer.synchronize(
            existing: [bootstrap, withdrawn], bootstrapProfileID: bootstrapID, catalog: catalog,
            makeUUID: { generated.removeFirst() }
        )
        #expect(profiles.count == 2)
        let master = try #require(profiles.first(where: { $0.clusterNodeID == "master" }))
        #expect(master.id == bootstrapID)
        #expect(master.region == "Russia")
        let edge = try #require(profiles.first(where: { $0.clusterNodeID == "edge" }))
        #expect(edge.serverIdentity == "edge.example.com")
        #expect(edge.region == "Finland")
        #expect(!edge.clusterAvailable)
        #expect(edge.managedByCluster)
        #expect(!profiles.contains(where: { $0.clusterNodeID == "old" }))
    }
}

private extension Data {
    func base64URLEncodedString() -> String {
        base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
}
