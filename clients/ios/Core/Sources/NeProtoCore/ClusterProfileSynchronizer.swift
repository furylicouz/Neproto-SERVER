import Foundation

public enum ClusterProfileSynchronizationError: Error, Equatable {
    case bootstrapProfileNotFound
    case bootstrapProfileNotPinned
    case catalogClusterMismatch
    case serverHasNoAddress(String)
    case serverHasNoTransportPath(String)
}

public enum ClusterProfileSynchronizer {
    public static func synchronize(
        existing: [ServerProfile],
        bootstrapProfileID: UUID,
        catalog: ClusterCatalog,
        makeUUID: () -> UUID = UUID.init
    ) throws -> [ServerProfile] {
        guard let bootstrap = existing.first(where: { $0.id == bootstrapProfileID }) else {
            throw ClusterProfileSynchronizationError.bootstrapProfileNotFound
        }
        guard let clusterID = bootstrap.clusterID, let publicKey = bootstrap.catalogPublicKey else {
            throw ClusterProfileSynchronizationError.bootstrapProfileNotPinned
        }
        guard clusterID == catalog.clusterID else {
            throw ClusterProfileSynchronizationError.catalogClusterMismatch
        }

        let manualProfiles = existing.filter { profile in
            !profile.managedByCluster && profile.id != bootstrapProfileID
        }
        var existingManaged: [String: ServerProfile] = [:]
        for profile in existing where profile.clusterID == clusterID {
            guard let nodeID = profile.clusterNodeID, existingManaged[nodeID] == nil else { continue }
            existingManaged[nodeID] = profile
        }
        let bootstrapNodeID = catalog.servers.first(where: { $0.serverIdentity == bootstrap.serverIdentity })?.nodeID

        var synchronized: [ServerProfile] = []
        synchronized.reserveCapacity(catalog.servers.count)
        for server in catalog.servers {
            let previous: ServerProfile?
            if server.nodeID == bootstrapNodeID {
                previous = bootstrap
            } else {
                previous = existingManaged[server.nodeID]
            }
            guard let address = server.serverAddresses.first ?? previous?.serverAddress ?? bootstrap.serverAddress else {
                throw ClusterProfileSynchronizationError.serverHasNoAddress(server.nodeID)
            }
            guard let httpsPath = server.httpsPath ?? previous?.httpsPath ?? nonEmpty(bootstrap.httpsPath),
                  let webRTCPath = server.webRTCPath ?? previous?.webRTCPath ?? nonEmpty(bootstrap.webRTCPath) else {
                throw ClusterProfileSynchronizationError.serverHasNoTransportPath(server.nodeID)
            }
            let profile = ServerProfile(
                id: previous?.id ?? makeUUID(),
                credentialID: bootstrap.credentialID,
                name: server.name,
                serverIdentity: server.serverIdentity,
                serverAddress: address,
                httpsPath: httpsPath,
                webRTCPath: webRTCPath,
                http3Path: server.http3Path ?? previous?.http3Path ?? bootstrap.http3Path,
                requireDatagrams: server.requireDatagrams,
                maxParallelCarriers: previous?.maxParallelCarriers ?? bootstrap.maxParallelCarriers,
                enableConstellation: previous?.enableConstellation ?? bootstrap.enableConstellation,
                enableForwardSecrecy: previous?.enableForwardSecrecy ?? bootstrap.enableForwardSecrecy,
                clusterID: clusterID,
                catalogPublicKey: publicKey,
                clusterNodeID: server.nodeID,
                managedByCluster: true,
                region: server.region,
                clusterAvailable: server.enabled,
                coverProfile: previous?.coverProfile ?? bootstrap.coverProfile
            )
            synchronized.append(profile)
        }

        return (manualProfiles + synchronized).sorted {
            if $0.managedByCluster != $1.managedByCluster { return $0.managedByCluster }
            return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
        }
    }

    private static func nonEmpty(_ value: String) -> String? {
        value.isEmpty ? nil : value
    }
}
