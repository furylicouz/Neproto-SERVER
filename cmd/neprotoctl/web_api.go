package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/geodata"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/selfupdate"
	usagestore "neproto.local/chameleon/internal/usage"
)

const webAPIMaximumBodyBytes = 64 * 1024

const webAPIUpdateCheckTimeout = 12 * time.Second

type webAPIUpdateChecker func(context.Context) (selfupdate.Status, error)

type webAPI struct {
	manager         *admin.Manager
	controller      serviceController
	syncCredentials clusterCredentialSynchronizer
	discoverHostKey func(clusterEnrollmentDraft) (string, error)
	enrolNode       func(*admin.Manager, serviceController, clusterEnrollmentDraft, io.Writer, io.Writer, func(int, string)) error
	jobs            *webAPIJobStore
	checkUpdate     webAPIUpdateChecker
	mutation        sync.Mutex
}

type webAPIError struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

type webAPIServiceSnapshot struct {
	NP2     string `json:"np2"`
	Ingress string `json:"ingress"`
	Web     string `json:"web"`
}

type webAPIHostSnapshot struct {
	Hostname       string `json:"hostname"`
	Uptime         string `json:"uptime"`
	Load           string `json:"load"`
	Memory         string `json:"memory"`
	MemoryPercent  uint64 `json:"memory_percent"`
	NetworkRX      string `json:"network_rx"`
	NetworkTX      string `json:"network_tx"`
	NetworkRXBytes uint64 `json:"network_rx_bytes"`
	NetworkTXBytes uint64 `json:"network_tx_bytes"`
}

type webAPIOverview struct {
	Version              string                `json:"version"`
	Deployment           string                `json:"deployment"`
	Domain               string                `json:"domain"`
	ServerAddresses      []string              `json:"server_addresses"`
	WebEnabled           bool                  `json:"web_enabled"`
	WebDomain            string                `json:"web_domain,omitempty"`
	WebPort              int                   `json:"web_port,omitempty"`
	EnableConstellation  bool                  `json:"enable_constellation"`
	EnableForwardSecrecy bool                  `json:"enable_forward_secrecy"`
	ActiveUsers          int                   `json:"active_users"`
	RevokedUsers         int                   `json:"revoked_users"`
	Backups              int                   `json:"backups"`
	ClusterRevision      uint64                `json:"cluster_revision"`
	ClusterNodes         int                   `json:"cluster_nodes"`
	HealthyClusterNodes  int                   `json:"healthy_cluster_nodes"`
	Routes               int                   `json:"routes"`
	EnabledRoutes        int                   `json:"enabled_routes"`
	GeoDataState         string                `json:"geodata_state"`
	GeoDataSchedule      string                `json:"geodata_schedule"`
	Services             webAPIServiceSnapshot `json:"services"`
	Host                 webAPIHostSnapshot    `json:"host"`
}

type webAPIUser struct {
	admin.User
	Online          bool                        `json:"online"`
	LastSeen        *time.Time                  `json:"last_seen,omitempty"`
	ActiveSessions  uint64                      `json:"active_sessions"`
	OnlineDevices   int                         `json:"online_devices"`
	EnrolledDevices int                         `json:"enrolled_devices"`
	UploadBytes     uint64                      `json:"upload_bytes"`
	DownloadBytes   uint64                      `json:"download_bytes"`
	TotalBytes      uint64                      `json:"total_bytes"`
	Devices         []usagestore.DeviceSnapshot `json:"devices"`
}

func newWebAPIHandler(root string, controller serviceController) (http.Handler, error) {
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		return nil, err
	}
	if controller == nil {
		controller = commandController{mode: manager.Installation().Mode}
	}
	updateEngine := selfupdate.NewEngine(
		buildinfo.Version,
		root,
		filepath.Join(root, "var", "lib", "neproto", "update"),
	)
	api := &webAPI{
		manager: manager, controller: controller, syncCredentials: syncClusterUserCredentialToNodes,
		discoverHostKey: discoverClusterEnrollmentHostKeyForDraft, enrolNode: enrolClusterNodeDraft,
		checkUpdate: updateEngine.CheckAvailability,
	}
	api.jobs = newWebAPIJobStore(&api.mutation)
	return api, nil
}

func (api *webAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if request.URL.Path == "/v1/overview" && request.Method == http.MethodGet {
		api.writeOverview(writer)
		return
	}
	if request.URL.Path == "/v1/system/update/check" {
		if request.Method != http.MethodPost {
			api.writeMethodNotAllowed(writer)
			return
		}
		api.checkUpdateAvailability(writer, request)
		return
	}
	if request.URL.Path == "/v1/users" {
		switch request.Method {
		case http.MethodGet:
			api.writeUsers(writer)
		case http.MethodPost:
			api.createUser(writer, request)
		default:
			api.writeMethodNotAllowed(writer)
		}
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/users/") {
		api.handleUser(writer, request)
		return
	}
	if request.URL.Path == "/v1/cluster" && request.Method == http.MethodGet {
		api.writeCluster(writer)
		return
	}
	if request.URL.Path == "/v1/cluster/host-key" && request.Method == http.MethodPost {
		api.startHostKeyDiscovery(writer, request)
		return
	}
	if request.URL.Path == "/v1/cluster/enrol" && request.Method == http.MethodPost {
		api.startClusterEnrolment(writer, request)
		return
	}
	if request.URL.Path == "/v1/cluster/sync-users" && request.Method == http.MethodPost {
		api.syncClusterUsers(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/cluster/nodes/") {
		api.handleClusterNode(writer, request)
		return
	}
	if request.URL.Path == "/v1/routes" {
		switch request.Method {
		case http.MethodGet:
			api.writeRoutes(writer)
		case http.MethodPost:
			api.createRoute(writer, request)
		default:
			api.writeMethodNotAllowed(writer)
		}
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/routes/") {
		api.handleRoute(writer, request)
		return
	}
	if request.URL.Path == "/v1/geodata" && request.Method == http.MethodGet {
		api.writeGeoData(writer)
		return
	}
	if request.URL.Path == "/v1/geodata/update" && request.Method == http.MethodPost {
		api.startGeoDataUpdate(writer, request)
		return
	}
	if request.URL.Path == "/v1/geodata/schedule" && request.Method == http.MethodPost {
		api.setGeoDataSchedule(writer, request)
		return
	}
	if request.URL.Path == "/v1/doctor" && request.Method == http.MethodPost {
		api.runWebDoctor(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/jobs/") && request.Method == http.MethodGet {
		api.writeJob(writer, request)
		return
	}
	if request.URL.Path == "/v1/services" && request.Method == http.MethodGet {
		api.writeServices(writer)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/services/") && request.Method == http.MethodPost {
		api.serviceAction(writer, request)
		return
	}
	if request.URL.Path == "/v1/logs" && request.Method == http.MethodGet {
		api.writeLogs(writer)
		return
	}
	if request.URL.Path == "/v1/settings" && request.Method == http.MethodGet {
		api.writeSettings(writer)
		return
	}
	if request.URL.Path == "/v1/settings/policy" && request.Method == http.MethodPost {
		api.setPolicy(writer, request)
		return
	}
	if request.URL.Path == "/v1/settings/domain" && request.Method == http.MethodPost {
		api.startDomainChange(writer, request)
		return
	}
	if request.URL.Path == "/v1/backups" {
		switch request.Method {
		case http.MethodGet:
			api.writeBackups(writer)
		case http.MethodPost:
			api.createBackup(writer, request)
		default:
			api.writeMethodNotAllowed(writer)
		}
		return
	}
	if request.URL.Path == "/v1/backups/restore" && request.Method == http.MethodPost {
		api.startBackupRestore(writer, request)
		return
	}
	api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
}

func (api *webAPI) checkUpdateAvailability(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), webAPIUpdateCheckTimeout)
	defer cancel()
	status, err := api.checkUpdate(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			api.writeError(writer, http.StatusGatewayTimeout, "update_check_timeout", "release check timed out")
			return
		}
		api.writeError(writer, http.StatusBadGateway, "update_check_failed", "release metadata is unavailable")
		return
	}
	api.writeJSON(writer, http.StatusOK, status)
}

func (api *webAPI) writeOverview(writer http.ResponseWriter) {
	users, err := api.manager.ListUsers()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	backups, err := api.manager.ListBackups()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	activeUsers, revokedUsers := managedUserCounts(users)
	overview := webAPIOverview{
		Version: buildinfo.Version, ActiveUsers: activeUsers, RevokedUsers: revokedUsers, Backups: len(backups),
		GeoDataSchedule: geoDataSchedule(api.manager.RootDirectory()),
	}
	installation := api.manager.Installation()
	overview.Deployment = installation.Mode
	overview.Domain = installation.Domain
	overview.ServerAddresses = append([]string(nil), installation.ServerAddresses...)
	overview.WebEnabled = installation.WebEnabled
	overview.WebDomain = installation.WebDomain
	overview.WebPort = installation.WebPort
	overview.EnableConstellation = installation.EnableConstellation
	overview.EnableForwardSecrecy = installation.EnableForwardSecrecy
	services := api.controller.Snapshot()
	overview.Services = webAPIServiceSnapshot{NP2: services.NP2, Ingress: services.Ingress, Web: services.Web}
	host := collectLinuxHostMetrics()
	overview.Host = webAPIHostSnapshot{
		Hostname: host.Hostname, Uptime: host.Uptime, Load: host.Load, Memory: host.Memory,
		MemoryPercent: host.MemoryPercent, NetworkRX: host.NetworkRX, NetworkTX: host.NetworkTX,
		NetworkRXBytes: host.NetworkRXBytes, NetworkTXBytes: host.NetworkTXBytes,
	}
	if state, clusterErr := api.manager.ClusterState(); clusterErr == nil {
		overview.ClusterRevision = state.Revision
		overview.ClusterNodes = len(state.Nodes)
		overview.Routes = len(state.Routes)
		for _, route := range state.Routes {
			if route.Enabled {
				overview.EnabledRoutes++
			}
		}
		for _, health := range probeClusterNodes(state.Nodes, 1200_000_000) {
			if health.status == "UP" {
				overview.HealthyClusterNodes++
			}
		}
	} else if !errors.Is(clusterErr, cluster.ErrStateNotFound) {
		api.writeInternalError(writer, clusterErr)
		return
	}
	if status, statusErr := geodata.Status(api.manager.GeodataDirectory()); statusErr == nil {
		overview.GeoDataState = string(status.State)
	} else {
		overview.GeoDataState = "unavailable"
	}
	api.writeJSON(writer, http.StatusOK, overview)
}

func (api *webAPI) writeUsers(writer http.ResponseWriter) {
	users, err := api.manager.ListUsers()
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	usageSnapshot, err := usagestore.ReadSnapshot(api.manager.UsageStatePath())
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	usageByUser := make(map[string]usagestore.UserSnapshot, len(usageSnapshot.Users))
	for _, snapshot := range usageSnapshot.Users {
		usageByUser[snapshot.UserID] = snapshot
	}
	result := make([]webAPIUser, 0, len(users))
	for _, user := range users {
		usage := usageByUser[user.ID]
		devices := append([]usagestore.DeviceSnapshot(nil), usage.Devices...)
		if devices == nil {
			devices = []usagestore.DeviceSnapshot{}
		}
		result = append(result, webAPIUser{
			User: user, Online: usage.Online, LastSeen: usage.LastSeen,
			ActiveSessions: usage.ActiveSessions, OnlineDevices: usage.OnlineDevices,
			EnrolledDevices: usage.EnrolledDevices, UploadBytes: usage.UploadBytes,
			DownloadBytes: usage.DownloadBytes, TotalBytes: usage.TotalBytes, Devices: devices,
		})
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"users": result})
}

func (api *webAPI) createUser(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Profile string `json:"profile"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	user, err := api.manager.AddUser(input.Name, input.Profile)
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	var output, failures bytes.Buffer
	if err := api.controller.Action("restart", &output, &failures); err != nil {
		api.writeError(writer, http.StatusInternalServerError, "service_restart_failed", "credential created but the NP/2 service restart failed")
		return
	}
	uri, err := api.manager.ExportUserURI(user.ID)
	if err != nil {
		api.writeInternalError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusCreated, map[string]any{"user": user, "uri": uri})
}

func (api *webAPI) handleUser(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/v1/users/"), "/")
	if len(parts) == 0 || len(parts) > 3 || parts[0] == "" {
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	identifier := parts[0]
	if len(parts) == 2 && request.Method == http.MethodPatch && parts[1] == "policy" {
		api.setUserPolicy(writer, request, identifier)
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost && parts[1] == "traffic-reset" {
		api.resetUserTraffic(writer, request, identifier)
		return
	}
	if len(parts) == 3 && request.Method == http.MethodDelete && parts[1] == "devices" {
		api.removeUserDevice(writer, identifier, parts[2])
		return
	}
	if len(parts) == 2 && request.Method == http.MethodGet && parts[1] == "export" {
		api.exportUser(writer, request, identifier)
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost && (parts[1] == "rotate" || parts[1] == "revoke") {
		api.mutateUser(writer, request, identifier, parts[1])
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost && parts[1] == "cluster-access" {
		api.setUserClusterAccess(writer, request, identifier)
		return
	}
	if len(parts) == 1 && request.Method == http.MethodDelete {
		api.deleteUser(writer, request, identifier)
		return
	}
	api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
}

func (api *webAPI) setUserPolicy(writer http.ResponseWriter, request *http.Request, identifier string) {
	var input struct {
		MaxDevices int `json:"max_devices"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	user, err := api.manager.SetUserDeviceLimit(identifier, input.MaxDevices)
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "user": user})
}

func (api *webAPI) resetUserTraffic(writer http.ResponseWriter, request *http.Request, identifier string) {
	var input struct{}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	user, err := api.manager.ResetUserTraffic(identifier)
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	if err := usagestore.ResetTraffic(api.manager.UsageStatePath(), identifier); err != nil {
		api.writeInternalError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "user": user})
}

func (api *webAPI) removeUserDevice(writer http.ResponseWriter, identifier, rawDeviceID string) {
	var deviceID protocol.DeviceID
	if err := deviceID.UnmarshalText([]byte(rawDeviceID)); err != nil {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "device identity is invalid")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if err := usagestore.RemoveOfflineDevice(api.manager.UsageStatePath(), identifier, deviceID); err != nil {
		switch {
		case errors.Is(err, usagestore.ErrDeviceOnline):
			api.writeError(writer, http.StatusConflict, "device_online", "disconnect the device before removing it")
		case errors.Is(err, usagestore.ErrDeviceNotFound):
			api.writeError(writer, http.StatusNotFound, "device_not_found", "device not found")
		case errors.Is(err, usagestore.ErrInvalidConfig), errors.Is(err, usagestore.ErrInvalidState):
			api.writeError(writer, http.StatusBadRequest, "invalid_request", "request contains invalid data")
		default:
			api.writeInternalError(writer, err)
		}
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": identifier, "device_id": rawDeviceID})
}

func (api *webAPI) exportUser(writer http.ResponseWriter, request *http.Request, identifier string) {
	format := request.URL.Query().Get("format")
	if format == "" || format == "uri" {
		uri, err := api.manager.ExportUserURI(identifier)
		if err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]string{"format": "uri", "value": uri})
		return
	}
	if format == "json" {
		profile, err := api.manager.ExportUserProfile(identifier)
		if err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]any{"format": "json", "profile": profile})
		return
	}
	if format == "manual" {
		profile, err := api.manager.ExportUserProfile(identifier)
		if err != nil {
			api.writeOperationError(writer, err)
			return
		}
		var manual bytes.Buffer
		writeManualProfile(&manual, profile)
		api.writeJSON(writer, http.StatusOK, map[string]string{"format": "manual", "value": manual.String()})
		return
	}
	if format == "qr" {
		uri, err := api.manager.ExportUserURI(identifier)
		if err != nil {
			api.writeOperationError(writer, err)
			return
		}
		var svg limitedBuffer
		svg.maximum = 512 * 1024
		if err := runQR([]string{"-t", "SVG", "-o", "-"}, uri, &svg, io.Discard); err != nil {
			api.writeError(writer, http.StatusServiceUnavailable, "qr_unavailable", "QR generator is unavailable")
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]string{
			"format": "qr", "mime": "image/svg+xml", "base64": base64.StdEncoding.EncodeToString([]byte(svg.String())),
		})
		return
	}
	api.writeError(writer, http.StatusBadRequest, "invalid_request", "format must be uri, json, manual or qr")
}

func (api *webAPI) mutateUser(writer http.ResponseWriter, request *http.Request, identifier, action string) {
	var input struct{}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	var err error
	if action == "rotate" {
		err = api.manager.RotateUser(identifier)
	} else {
		err = api.manager.RevokeUser(identifier)
	}
	if err != nil {
		api.writeOperationError(writer, err)
		return
	}
	if err := api.syncUserCredential(identifier); err != nil {
		api.writeError(writer, http.StatusBadGateway, "cluster_sync_failed", "credential changed locally but cluster synchronization failed")
		return
	}
	if err := api.controller.Action("restart", io.Discard, io.Discard); err != nil {
		api.writeError(writer, http.StatusInternalServerError, "service_restart_failed", "credential changed but the NP/2 service restart failed")
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "action": action, "id": identifier})
}

func (api *webAPI) deleteUser(writer http.ResponseWriter, request *http.Request, identifier string) {
	var input struct {
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "DELETE" {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type DELETE to permanently remove the revoked user")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if err := api.manager.DeleteUser(identifier); err != nil {
		api.writeOperationError(writer, err)
		return
	}
	if err := api.syncUserCredential(identifier); err != nil {
		api.writeError(writer, http.StatusBadGateway, "cluster_sync_failed", "user deleted locally but cluster synchronization failed")
		return
	}
	if err := api.controller.Action("restart", io.Discard, io.Discard); err != nil {
		api.writeError(writer, http.StatusInternalServerError, "service_restart_failed", "user deleted but the NP/2 service restart failed")
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": identifier})
}

func (api *webAPI) syncUserCredential(userID string) error {
	state, err := api.manager.ClusterState()
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
	return api.syncCredentials(api.manager, userID, allowed)
}

func (api *webAPI) decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	raw, err := io.ReadAll(io.LimitReader(request.Body, webAPIMaximumBodyBytes+1))
	if err != nil {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "cannot read request body")
		return false
	}
	if len(raw) > webAPIMaximumBodyBytes {
		api.writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func (api *webAPI) writeOperationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrUserNotFound):
		api.writeError(writer, http.StatusNotFound, "user_not_found", "user not found")
	case errors.Is(err, admin.ErrUserMustBeRevoked):
		api.writeError(writer, http.StatusConflict, "user_must_be_revoked", "revoke the user before deletion")
	case errors.Is(err, admin.ErrLastActiveUser):
		api.writeError(writer, http.StatusConflict, "last_active_user", "create another active user before revoking this one")
	case errors.Is(err, admin.ErrInvalidUser), errors.Is(err, admin.ErrInvalidState), errors.Is(err, cluster.ErrInvalidState):
		api.writeError(writer, http.StatusBadRequest, "invalid_request", "request contains invalid data")
	default:
		api.writeInternalError(writer, err)
	}
}

func (api *webAPI) writeInternalError(writer http.ResponseWriter, err error) {
	_ = err
	api.writeError(writer, http.StatusInternalServerError, "operation_failed", "operation failed; review sanitized service events")
}

func (api *webAPI) writeMethodNotAllowed(writer http.ResponseWriter) {
	api.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func (api *webAPI) writeError(writer http.ResponseWriter, status int, category, message string) {
	api.writeJSON(writer, status, webAPIError{Error: category, Message: message})
}

func (api *webAPI) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fmt.Fprintln(writer, `{"error":"response_failed"}`)
	}
}
