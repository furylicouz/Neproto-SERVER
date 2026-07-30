package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/geodata"
)

const webAPIMaximumLogBytes = 256 * 1024

type webAPIClusterNode struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Region          string             `json:"region"`
	Roles           []cluster.NodeRole `json:"roles"`
	PublicIdentity  string             `json:"public_identity"`
	PublicAddresses []string           `json:"public_addresses"`
	NP2Endpoint     string             `json:"np2_endpoint"`
	Enabled         bool               `json:"enabled"`
	ClientVisible   bool               `json:"client_visible"`
	ProvisionedAt   time.Time          `json:"provisioned_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Health          string             `json:"health"`
	LatencyMS       int64              `json:"latency_ms"`
	CheckedAt       time.Time          `json:"checked_at"`
}

func (api *webAPI) writeCluster(writer http.ResponseWriter) {
	state, err := api.manager.EnsureLocalCluster()
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	health := probeClusterNodes(state.Nodes, 1200*time.Millisecond)
	nodes := make([]webAPIClusterNode, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		status := health[node.ID]
		nodes = append(nodes, webAPIClusterNode{
			ID: node.ID, Name: node.Name, Region: node.Region, Roles: append([]cluster.NodeRole(nil), node.Roles...),
			PublicIdentity: node.PublicIdentity, PublicAddresses: append([]string(nil), node.PublicAddresses...),
			NP2Endpoint: node.NP2Endpoint, Enabled: node.Enabled, ClientVisible: node.ClientVisible,
			ProvisionedAt: node.ProvisionedAt, UpdatedAt: node.UpdatedAt, Health: status.status,
			LatencyMS: status.latency.Milliseconds(), CheckedAt: status.checked,
		})
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{
		"cluster_id": state.ClusterID, "revision": state.Revision, "nodes": nodes, "access": state.Access,
	})
}

type webAPIRouteInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Priority int    `json:"priority"`
	Match    struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"match"`
	Action struct {
		Kind    string   `json:"kind"`
		NodeIDs []string `json:"node_ids"`
	} `json:"action"`
	UserIDs []string `json:"user_ids"`
}

func (api *webAPI) writeRoutes(writer http.ResponseWriter) {
	state, err := api.manager.EnsureLocalCluster()
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	status, statusErr := geodata.Status(api.manager.GeodataDirectory())
	if statusErr != nil {
		status.State = geodata.UpdateStateError
		status.Error = "GeoData is unavailable"
	}
	routes := state.Routes
	if routes == nil {
		routes = []cluster.Route{}
	}
	access := state.Access
	if access == nil {
		access = []cluster.UserAccess{}
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{
		"revision": state.Revision, "routes": routes, "access": access,
		"geodata": status, "schedule": geoDataSchedule(api.manager.RootDirectory()),
	})
}

func (api *webAPI) createRoute(writer http.ResponseWriter, request *http.Request) {
	var input webAPIRouteInput
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	draft := clusterRouteDraft{
		id: input.ID, name: input.Name, priority: input.Priority,
		match:   strings.ToLower(strings.TrimSpace(input.Match.Kind)) + ":" + strings.TrimSpace(input.Match.Value),
		userIDs: append([]string(nil), input.UserIDs...),
	}
	action := strings.ToLower(strings.TrimSpace(input.Action.Kind))
	if action == "node" || action == "chain" {
		draft.action = action + ":" + strings.Join(input.Action.NodeIDs, ",")
	} else {
		draft.action = action
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if err := createClusterRouteForUsersSynced(api.manager, draft, api.syncCredentials); err != nil {
		api.writeOperationError(writer, err)
		return
	}
	state, err := api.manager.ClusterState()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	for _, route := range state.Routes {
		if route.ID == input.ID {
			api.writeJSON(writer, http.StatusCreated, map[string]any{"route": route, "revision": state.Revision})
			return
		}
	}
	api.writeInternalError(writer, admin.ErrClusterRouteNotFound)
}

func (api *webAPI) writeServices(writer http.ResponseWriter) {
	services := api.controller.Snapshot()
	api.writeJSON(writer, http.StatusOK, map[string]any{
		"services": webAPIServiceSnapshot{NP2: services.NP2, Ingress: services.Ingress, Web: services.Web},
	})
}

func (api *webAPI) serviceAction(writer http.ResponseWriter, request *http.Request) {
	action := strings.TrimPrefix(request.URL.Path, "/v1/services/")
	if action != "start" && action != "stop" && action != "restart" && action != "validate" {
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	var input struct {
		Confirm string `json:"confirm,omitempty"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if action == "stop" && input.Confirm != "STOP" {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type STOP to stop all NeProto services")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	var output, failures bytes.Buffer
	var err error
	if action == "validate" {
		err = api.controller.Validate(&output, &failures)
	} else {
		err = api.controller.Action(action, &output, &failures)
	}
	if err != nil {
		api.writeError(writer, http.StatusInternalServerError, "service_action_failed", "service action failed; review sanitized events")
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "action": action})
}

func (api *webAPI) writeLogs(writer http.ResponseWriter) {
	var output limitedBuffer
	output.maximum = webAPIMaximumLogBytes
	var failures limitedBuffer
	failures.maximum = webAPIMaximumLogBytes
	if err := api.controller.Logs(false, &output, &failures); err != nil {
		api.writeError(writer, http.StatusInternalServerError, "logs_unavailable", "service events are unavailable")
		return
	}
	combined := output.String() + failures.String()
	api.writeJSON(writer, http.StatusOK, map[string]any{"lines": splitTUIOutput(combined, 400)})
}

func (api *webAPI) writeSettings(writer http.ResponseWriter) {
	installation := api.manager.Installation()
	api.writeJSON(writer, http.StatusOK, map[string]any{
		"deployment": installation.Mode, "domain": installation.Domain,
		"server_addresses": installation.ServerAddresses, "web_enabled": installation.WebEnabled,
		"web_domain": installation.WebDomain, "web_port": installation.WebPort,
		"enable_constellation":   installation.EnableConstellation,
		"enable_forward_secrecy": installation.EnableForwardSecrecy,
	})
}

func (api *webAPI) setPolicy(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Mode    string `json:"mode"`
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if (input.Mode != "production" && input.Mode != "compatibility") || input.Confirm != strings.ToUpper(input.Mode) {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "confirm the selected production or compatibility policy")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	var output, failures bytes.Buffer
	if code := performFeatureSet(api.manager, api.controller, input.Mode == "production", &output, &failures); code != 0 {
		api.writeError(writer, http.StatusInternalServerError, "policy_update_failed", "policy update failed and rollback was attempted")
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "mode": input.Mode})
}

func (api *webAPI) writeBackups(writer http.ResponseWriter) {
	paths, err := api.manager.ListBackups()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	backups := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		backups = append(backups, map[string]string{"id": filepath.Base(path), "name": filepath.Base(path)})
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"backups": backups})
}

func (api *webAPI) createBackup(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	path, err := api.manager.CreateBackup()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusCreated, map[string]string{"id": filepath.Base(path), "name": filepath.Base(path)})
}

type limitedBuffer struct {
	buffer  bytes.Buffer
	maximum int
	full    bool
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if buffer.full || buffer.maximum <= 0 {
		return len(value), nil
	}
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.full = true
		return len(value), nil
	}
	write := value
	if len(write) > remaining {
		write = write[:remaining]
		buffer.full = true
	}
	_, _ = buffer.buffer.Write(write)
	return len(value), nil
}

func (buffer *limitedBuffer) String() string { return buffer.buffer.String() }

var _ io.Writer = (*limitedBuffer)(nil)

func (api *webAPI) clusterState() (cluster.State, error) {
	state, err := api.manager.ClusterState()
	if errors.Is(err, cluster.ErrStateNotFound) {
		return api.manager.EnsureLocalCluster()
	}
	return state, err
}
