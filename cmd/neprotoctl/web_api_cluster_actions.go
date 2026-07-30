package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
)

func (api *webAPI) startHostKeyDiscovery(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if len(input.Host) == 0 || len(input.Host) > 253 || input.Port < 1 || input.Port > 65535 ||
		len(input.User) == 0 || len(input.User) > 64 || len(input.Password) == 0 || len(input.Password) > 1024 {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "invalid SSH connection settings")
		return
	}
	draft := clusterEnrollmentDraft{host: input.Host, port: uint16(input.Port), user: input.User, password: []byte(input.Password)}
	input.Password = ""
	api.startJob(writer, "cluster_host_key", func(report func(int, string)) (any, error) {
		defer zeroClusterEnrollmentDraft(&draft)
		report(20, "Connecting to the new server over SSH")
		fingerprint, err := api.discoverHostKey(draft)
		if err != nil {
			return nil, sanitizedEnrollmentError(err, draft.password)
		}
		report(95, "SSH host key received")
		return map[string]string{"fingerprint": fingerprint}, nil
	})
}

func (api *webAPI) startClusterEnrolment(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Host        string   `json:"host"`
		Port        int      `json:"port"`
		User        string   `json:"user"`
		Password    string   `json:"password"`
		Fingerprint string   `json:"fingerprint"`
		NodeID      string   `json:"node_id"`
		Name        string   `json:"name"`
		Region      string   `json:"region"`
		Domain      string   `json:"domain"`
		Addresses   []string `json:"addresses"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if len(input.Host) == 0 || len(input.Host) > 253 || input.Port < 1 || input.Port > 65535 ||
		len(input.User) == 0 || len(input.User) > 64 || len(input.Password) == 0 || len(input.Password) > 1024 ||
		len(input.Fingerprint) < 8 || len(input.Fingerprint) > 256 || !strings.HasPrefix(input.Fingerprint, "SHA256:") ||
		len(input.NodeID) == 0 || len(input.NodeID) > 64 || len(input.Name) == 0 || len(input.Name) > 96 ||
		len(input.Region) == 0 || len(input.Region) > 96 || len(input.Domain) == 0 || len(input.Domain) > 253 ||
		len(input.Addresses) == 0 || len(input.Addresses) > 8 {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "invalid cluster enrolment settings")
		return
	}
	draft := clusterEnrollmentDraft{
		host: input.Host, port: uint16(input.Port), user: input.User, password: []byte(input.Password),
		fingerprint: input.Fingerprint, nodeID: input.NodeID, name: input.Name, region: input.Region,
		domain: input.Domain, addresses: append([]string(nil), input.Addresses...),
	}
	input.Password = ""
	api.startJob(writer, "cluster_enrolment", func(report func(int, string)) (any, error) {
		defer zeroClusterEnrollmentDraft(&draft)
		var output, failures bytes.Buffer
		if err := api.enrolNode(api.manager, api.controller, draft, &output, &failures, report); err != nil {
			return nil, sanitizedEnrollmentError(err, draft.password)
		}
		return map[string]any{"node_id": draft.nodeID, "completed": true}, nil
	})
}

func sanitizedEnrollmentError(err error, password []byte) error {
	message := err.Error()
	if len(password) > 0 {
		message = strings.ReplaceAll(message, string(password), "[redacted]")
	}
	return errors.New(boundedDisplay(message, 480))
}

func (api *webAPI) handleClusterNode(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/v1/cluster/nodes/"), "/")
	if len(parts) == 1 && parts[0] != "" && request.Method == http.MethodDelete {
		api.removeClusterNode(writer, request, parts[0])
		return
	}
	if len(parts) != 2 || parts[0] == "" || request.Method != http.MethodPost {
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	nodeID, action := parts[0], parts[1]
	switch action {
	case "enable", "publish":
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if !api.decodeJSON(writer, request, &input) {
			return
		}
		api.mutation.Lock()
		defer api.mutation.Unlock()
		if err := api.setClusterNodeFlag(nodeID, action, input.Enabled); err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": nodeID, "enabled": input.Enabled})
	case "assign-user":
		var input struct {
			UserID  string `json:"user_id"`
			Enabled bool   `json:"enabled"`
		}
		if !api.decodeJSON(writer, request, &input) {
			return
		}
		api.mutation.Lock()
		defer api.mutation.Unlock()
		if err := api.setClusterNodeForUser(nodeID, input.UserID, input.Enabled); err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "node_id": nodeID, "user_id": input.UserID, "enabled": input.Enabled})
	default:
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
	}
}

func (api *webAPI) syncClusterUsers(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	users, err := api.manager.ListUsers()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	for _, user := range users {
		if err := api.syncUserCredential(user.ID); err != nil {
			api.writeError(writer, http.StatusBadGateway, "cluster_sync_failed", "one or more credentials could not be synchronized")
			return
		}
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "synchronized_users": len(users)})
}

func (api *webAPI) setUserClusterAccess(writer http.ResponseWriter, request *http.Request, userID string) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	state, err := api.manager.ClusterState()
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	current := false
	for _, access := range state.Access {
		if access.UserID == userID {
			current = true
			break
		}
	}
	if current != input.Enabled {
		if _, err := toggleUserClusterAccessSynced(api.manager, userID, api.syncCredentials); err != nil {
			api.writeOperationError(writer, err)
			return
		}
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "user_id": userID, "enabled": input.Enabled})
}

func (api *webAPI) removeClusterNode(writer http.ResponseWriter, request *http.Request, nodeID string) {
	var input struct {
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "REMOVE" {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type REMOVE to remove the cluster node")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if err := mutateSelectedClusterNode(api.manager, tuiOperationClusterRemove, nodeID); err != nil {
		api.writeOperationError(writer, err)
		return
	}
	if err := api.controller.Action("restart", io.Discard, io.Discard); err != nil {
		api.writeError(writer, http.StatusInternalServerError, "service_restart_failed", "node removed but the NP/2 service restart failed")
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": nodeID})
}

func (api *webAPI) setClusterNodeFlag(nodeID, field string, enabled bool) error {
	state, err := api.manager.ClusterState()
	if err != nil {
		return err
	}
	for _, node := range state.Nodes {
		if node.ID != nodeID {
			continue
		}
		if containsClusterRole(node.Roles, cluster.RoleMaster) && !enabled {
			return errors.New("master node cannot be disabled or hidden")
		}
		if field == "enable" {
			if !enabled && node.ClientVisible {
				return errors.New("hide the node from clients before draining it")
			}
			node.Enabled = enabled
		} else {
			if enabled && !node.Enabled {
				return errors.New("enable the node before publishing it")
			}
			node.ClientVisible = enabled
		}
		_, err = api.manager.UpsertClusterNode(node)
		return err
	}
	return admin.ErrClusterNodeNotFound
}

func (api *webAPI) setClusterNodeForUser(nodeID, userID string, enabled bool) error {
	state, err := api.manager.ClusterState()
	if err != nil {
		return err
	}
	currentlyEnabled := false
	for _, access := range state.Access {
		if access.UserID == userID {
			currentlyEnabled = containsString(access.AllowedNodeIDs, nodeID)
			break
		}
	}
	if currentlyEnabled == enabled {
		return nil
	}
	_, err = toggleClusterNodeForUserSynced(api.manager, nodeID, userID, api.syncCredentials)
	return err
}
