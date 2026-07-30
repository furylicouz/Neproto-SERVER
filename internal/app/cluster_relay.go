package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/geodata"
	"neproto.local/chameleon/internal/proxy"
)

type clusterRelayServices struct {
	runtime        *clusterrelay.Runtime
	pool           *clusterrelay.PeerPool
	catalog        proxy.CatalogHandler
	catalogRelay   proxy.CatalogRelayHandler
	credentialSync proxy.CredentialSyncHandler
	clusterState   proxy.ClusterStateHandler
	geodataControl proxy.GeoDataControlHandler
}

func newClusterRelayServices(server config.Server, localCatalog proxy.CatalogHandler) (*clusterRelayServices, error) {
	if server.ClusterNodeID == "" {
		return nil, nil
	}
	peers, err := clusterrelay.LoadPeerConfigs(server.ClusterPeerDirectory)
	if err != nil {
		return nil, err
	}
	principals, err := clusterrelay.LoadAcceptedPeers(server.ClusterPeerMapFile)
	if err != nil {
		return nil, err
	}
	pool, err := clusterrelay.NewPeerPool(peers, ConnectClientHTTPSFirst)
	if err != nil {
		return nil, err
	}
	runtime := &clusterrelay.Runtime{
		NodeID: server.ClusterNodeID, MasterNodeID: server.ClusterMasterNodeID,
		PeerPrincipals: principals, OpenPeer: pool.Open, DialTarget: clusterrelay.DialTarget,
		DialUDP: func(ctx context.Context, target proxy.Target) (proxy.DuplexStream, error) {
			return proxy.NewUDPTerminalStream(ctx, target, proxy.MaxUDPDatagramPayload, 60*time.Second)
		},
	}
	var reloadable *geodata.Reloadable
	if server.GeodataDirectory != "" {
		matcher, loadErr := geodata.Load(server.GeodataDirectory)
		if loadErr != nil {
			_ = pool.Close()
			return nil, loadErr
		}
		reloadable = geodata.NewReloadable(matcher)
		runtime.GeoMatcher = reloadable
	}
	var store *cluster.Store
	if server.ClusterNodeID == server.ClusterMasterNodeID {
		store, err = cluster.OpenStore(filepath.Clean(server.ClusterDirectory))
		if err != nil {
			_ = pool.Close()
			return nil, err
		}
		runtime.LoadState = store.Load
	} else {
		stateCache, cacheErr := clusterrelay.NewStateCache(
			server.ClusterNodeID, server.ClusterMasterNodeID,
			func(ctx context.Context) ([]byte, error) {
				return pool.FetchState(ctx, server.ClusterMasterNodeID)
			},
		)
		if cacheErr != nil {
			_ = pool.Close()
			return nil, cacheErr
		}
		runtime.LoadState = stateCache.Load
	}
	if err := runtime.Validate(); err != nil {
		_ = pool.Close()
		return nil, err
	}
	services := &clusterRelayServices{runtime: runtime, pool: pool}
	if server.ClusterNodeID == server.ClusterMasterNodeID {
		if localCatalog == nil {
			_ = pool.Close()
			return nil, errors.New("cluster master catalog handler is unavailable")
		}
		services.catalog = localCatalog
		services.catalogRelay = func(ctx context.Context, credentialID, userID string) ([]byte, error) {
			if _, accepted := principals[credentialID]; !accepted {
				return nil, clusterrelay.ErrRelayUnauthorized
			}
			return localCatalog(ctx, userID)
		}
		services.clusterState = func(_ context.Context, credentialID string) ([]byte, error) {
			if _, accepted := principals[credentialID]; !accepted {
				return nil, clusterrelay.ErrRelayUnauthorized
			}
			state, loadErr := store.Load()
			if loadErr != nil {
				return nil, loadErr
			}
			payload, marshalErr := json.Marshal(state)
			if marshalErr != nil || len(payload) == 0 || len(payload) > proxy.MaxClusterStatePayload {
				return nil, cluster.ErrInvalidState
			}
			return payload, nil
		}
	} else {
		services.catalog = func(ctx context.Context, userID string) ([]byte, error) {
			return pool.FetchCatalog(ctx, server.ClusterMasterNodeID, userID)
		}
		services.credentialSync, err = clusterrelay.NewCredentialSyncHandler(
			server.ClusterNodeID, server.ClusterMasterNodeID, principals, server.CredentialDirectory,
		)
		if err != nil {
			_ = pool.Close()
			return nil, err
		}
		if reloadable != nil {
			updater := geodata.DefaultUpdater()
			services.geodataControl = newGeoDataControlHandler(
				server.ClusterNodeID, server.ClusterMasterNodeID, principals, server.GeodataDirectory,
				updater.Update, geodata.Status, reloadable.Reload,
			)
		}
	}
	return services, nil
}

type geoDataUpdateFunc func(context.Context, string) (geodata.UpdateStatus, error)
type geoDataStatusFunc func(string) (geodata.UpdateStatus, error)
type geoDataReloadFunc func(string) error

func newGeoDataControlHandler(
	nodeID, masterNodeID string,
	principals map[string]string,
	directory string,
	update geoDataUpdateFunc,
	status geoDataStatusFunc,
	reload geoDataReloadFunc,
) proxy.GeoDataControlHandler {
	return func(ctx context.Context, credentialID string, request cluster.GeoDataRequest) ([]byte, error) {
		if nodeID == "" || nodeID == masterNodeID || principals[credentialID] != masterNodeID ||
			cluster.ValidateGeoDataRequest(request) != nil || update == nil || status == nil || reload == nil {
			return nil, clusterrelay.ErrRelayUnauthorized
		}
		var current geodata.UpdateStatus
		var err error
		if request.Operation == cluster.GeoDataUpdate {
			current, err = update(ctx, directory)
			if err == nil {
				err = reload(directory)
			}
		} else {
			current, err = status(directory)
		}
		response := cluster.GeoDataNodeStatus{
			Version: cluster.GeoDataControlVersion, NodeID: nodeID, State: current.State,
			UpdatedAt: current.UpdatedAt, GeoIPSHA256: current.GeoIPSHA256, GeoSiteSHA256: current.GeoSiteSHA256,
			GeoIPBytes: current.GeoIPBytes, GeoSiteBytes: current.GeoSiteBytes,
		}
		if err != nil {
			response.State = geodata.UpdateStateError
			response.Error = boundedGeoDataError(err)
			if request.Operation == cluster.GeoDataUpdate {
				slog.Warn("NP/2 cluster geodata operation failed", "event", "cluster_geodata_failed", "node_id", nodeID, "operation", request.Operation, "reason", response.Error)
			}
		} else if request.Operation == cluster.GeoDataUpdate {
			slog.Info("NP/2 cluster geodata operation completed", "event", "cluster_geodata_completed", "node_id", nodeID, "operation", request.Operation, "geoip_sha256", response.GeoIPSHA256, "geosite_sha256", response.GeoSiteSHA256)
		}
		payload, marshalErr := json.Marshal(response)
		if marshalErr != nil || len(payload) == 0 || len(payload) > proxy.MaxGeoDataPayload {
			return nil, clusterrelay.ErrInvalidConfig
		}
		return payload, nil
	}
}

func boundedGeoDataError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func (services *clusterRelayServices) Close() error {
	if services == nil || services.pool == nil {
		return nil
	}
	return services.pool.Close()
}
