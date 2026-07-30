package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/config"
)

func syncClusterUserCredential(manager *admin.Manager, userID string) error {
	state, err := manager.ClusterState()
	if errors.Is(err, cluster.ErrStateNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	allowed := make([]string, 0)
	for _, access := range state.Access {
		if access.UserID == userID {
			allowed = append(allowed, access.AllowedNodeIDs...)
			break
		}
	}
	return syncClusterUserCredentialToNodes(manager, userID, allowed)
}

func syncClusterUserCredentialToNodes(manager *admin.Manager, userID string, allowedNodeIDs []string) error {
	server, err := config.LoadServer(manager.ServerConfigPath())
	if err != nil {
		return err
	}
	if server.ClusterNodeID == "" {
		return nil
	}
	peerConfigs, err := clusterrelay.LoadPeerConfigs(server.ClusterPeerDirectory)
	if err != nil {
		return err
	}
	pool, err := clusterrelay.NewPeerPool(peerConfigs, app.ConnectClientHTTPSFirst)
	if err != nil {
		return err
	}
	defer pool.Close()
	allowed := make(map[string]struct{}, len(allowedNodeIDs))
	for _, nodeID := range allowedNodeIDs {
		allowed[nodeID] = struct{}{}
	}
	secret, secretErr := manager.ActiveCredentialSecret(userID)
	request := cluster.CredentialSyncRequest{Version: cluster.CredentialSyncVersion, Operation: cluster.CredentialSyncRevoke, CredentialID: userID}
	if secretErr == nil {
		request.Operation = cluster.CredentialSyncUpsert
		request.Secret = secret
	} else if !errors.Is(secretErr, admin.ErrUserNotFound) {
		return secretErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var failures []error
	for nodeID := range peerConfigs {
		nodeRequest := request
		if _, permitted := allowed[nodeID]; !permitted {
			nodeRequest.Operation = cluster.CredentialSyncRevoke
			nodeRequest.Secret = ""
		}
		if err := pool.SyncCredential(ctx, nodeID, nodeRequest); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", nodeID, err))
		}
	}
	return errors.Join(failures...)
}
