package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/session"
)

type clusterPeerConnector func(context.Context, config.Client) (*session.Authenticated, error)

func attestInstalledClusterPeer(
	manager *admin.Manager,
	nodeID string,
	connect clusterPeerConnector,
) error {
	if manager == nil || nodeID == "" || connect == nil {
		return clusterrelay.ErrInvalidConfig
	}
	server, err := config.LoadServer(manager.ServerConfigPath())
	if err != nil {
		return err
	}
	peers, err := clusterrelay.LoadPeerConfigs(server.ClusterPeerDirectory)
	if err != nil {
		return err
	}
	peer, exists := peers[nodeID]
	if !exists {
		return clusterrelay.ErrPeerUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authenticated, err := connect(ctx, peer)
	if err != nil {
		return fmt.Errorf("authenticate installed NP/2 peer: %w", err)
	}
	if authenticated == nil || authenticated.Mux == nil {
		return clusterrelay.ErrPeerUnavailable
	}
	if err := authenticated.Mux.Close(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("close peer attestation session: %w", err)
	}
	return nil
}

func attestProductionClusterPeer(manager *admin.Manager, nodeID string) error {
	return attestInstalledClusterPeer(manager, nodeID, app.ConnectClientHTTPSFirst)
}
