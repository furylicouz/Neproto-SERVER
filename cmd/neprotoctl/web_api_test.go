package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/selfupdate"
	usagestore "neproto.local/chameleon/internal/usage"
)

func TestWebAPIUpdateCheckReturnsFreshTerminalStatus(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	api := handler.(*webAPI)
	api.checkUpdate = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{
			Schema: 1, State: "idle", CurrentVersion: "np2-0.5.5", AvailableVersion: "np2-0.5.6",
			UpdateAvailable: true, Progress: 0, Message: "Update available", UpdatedAt: "2026-07-30T12:00:00Z",
		}, nil
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/system/update/check", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"idle"`) ||
		!strings.Contains(response.Body.String(), `"available_version":"np2-0.5.6"`) {
		t.Fatalf("update check status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWebAPIUpdateCheckReturnsBoundedFailure(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	api := handler.(*webAPI)
	api.checkUpdate = func(context.Context) (selfupdate.Status, error) {
		return selfupdate.Status{}, context.DeadlineExceeded
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/system/update/check", nil))
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "update_check_timeout") {
		t.Fatalf("update check failure status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWebAPIOverviewUsesInstalledState(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active", Web: "active"}}
	handler := mustWebAPIHandler(t, root, controller)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/overview", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Domain       string `json:"domain"`
		ActiveUsers  int    `json:"active_users"`
		RevokedUsers int    `json:"revoked_users"`
		Services     struct {
			NP2     string `json:"np2"`
			Web     string `json:"web"`
			Ingress string `json:"ingress"`
		} `json:"services"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Domain != "vpn.example.com" || body.Services.NP2 != "active" || body.Services.Web != "active" || body.Services.Ingress != "active" {
		t.Fatalf("unexpected overview: %+v", body)
	}
	if strings.Contains(response.Body.String(), "private_https_route") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("overview leaked sensitive configuration: %s", response.Body.String())
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control=%q", cache)
	}
}

func TestWebAPIUserLifecycleReturnsExplicitCredentialExport(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active"}}
	handler := mustWebAPIHandler(t, root, controller)

	created := httptest.NewRecorder()
	handler.ServeHTTP(created, jsonRequest(t, http.MethodPost, "/v1/users", map[string]any{
		"name": "Alice iPhone", "profile": "web",
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var creation struct {
		User struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"user"`
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &creation); err != nil {
		t.Fatal(err)
	}
	if creation.User.ID == "" || creation.User.Name != "Alice iPhone" || creation.User.Status != "active" || !strings.HasPrefix(creation.URI, "np2://") {
		t.Fatalf("unexpected creation response: %+v", creation)
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("actions=%v", controller.actions)
	}

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), creation.User.ID) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, jsonRequest(t, http.MethodPost, "/v1/users/"+creation.User.ID+"/revoke", map[string]any{}))
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}

	rejectedDelete := httptest.NewRecorder()
	handler.ServeHTTP(rejectedDelete, jsonRequest(t, http.MethodDelete, "/v1/users/"+creation.User.ID, map[string]any{}))
	if rejectedDelete.Code != http.StatusBadRequest || !strings.Contains(rejectedDelete.Body.String(), "confirmation_required") {
		t.Fatalf("delete without confirmation status=%d body=%s", rejectedDelete.Code, rejectedDelete.Body.String())
	}

	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, jsonRequest(t, http.MethodDelete, "/v1/users/"+creation.User.ID, map[string]any{"confirm": "DELETE"}))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestWebAPIUserUsageDevicePolicyAndReset(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, jsonRequest(t, http.MethodPost, "/v1/users", map[string]any{
		"name": "Measured phone", "profile": "web",
	}))
	var creation struct {
		User admin.User `json:"user"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &creation); err != nil {
		t.Fatal(err)
	}

	policy := httptest.NewRecorder()
	handler.ServeHTTP(policy, jsonRequest(t, http.MethodPatch, "/v1/users/"+creation.User.ID+"/policy", map[string]any{"max_devices": 1}))
	if policy.Code != http.StatusOK || !strings.Contains(policy.Body.String(), `"max_devices":1`) {
		t.Fatalf("policy status=%d body=%s", policy.Code, policy.Body.String())
	}

	api := handler.(*webAPI)
	tracker, err := usagestore.New(usagestore.Config{
		PolicyPath: filepath.Join(root, "etc", "neproto", "users", "index.json"),
		StatePath:  api.manager.UsageStatePath(),
		Now:        time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	device := protocol.DeviceID{0x10, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x01}
	counters := usagestore.Counters{}
	session, err := tracker.Admit(creation.User.ID, device, func() usagestore.Counters { return counters })
	if err != nil {
		t.Fatal(err)
	}
	counters = usagestore.Counters{UploadBytes: 1024, DownloadBytes: 4096}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"online":true`) ||
		!strings.Contains(listed.Body.String(), `"upload_bytes":1024`) || !strings.Contains(listed.Body.String(), `"download_bytes":4096`) {
		t.Fatalf("usage list status=%d body=%s", listed.Code, listed.Body.String())
	}

	deviceText, _ := device.MarshalText()
	activeDelete := httptest.NewRecorder()
	handler.ServeHTTP(activeDelete, httptest.NewRequest(http.MethodDelete,
		"/v1/users/"+creation.User.ID+"/devices/"+string(deviceText), nil))
	if activeDelete.Code != http.StatusConflict {
		t.Fatalf("active device delete status=%d body=%s", activeDelete.Code, activeDelete.Body.String())
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	offlineDelete := httptest.NewRecorder()
	handler.ServeHTTP(offlineDelete, httptest.NewRequest(http.MethodDelete,
		"/v1/users/"+creation.User.ID+"/devices/"+string(deviceText), nil))
	if offlineDelete.Code != http.StatusOK {
		t.Fatalf("offline device delete status=%d body=%s", offlineDelete.Code, offlineDelete.Body.String())
	}

	reset := httptest.NewRecorder()
	handler.ServeHTTP(reset, jsonRequest(t, http.MethodPost, "/v1/users/"+creation.User.ID+"/traffic-reset", map[string]any{}))
	if reset.Code != http.StatusOK || !strings.Contains(reset.Body.String(), `"traffic_reset_generation":1`) {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body.String())
	}
	usageSnapshot, err := usagestore.ReadSnapshot(api.manager.UsageStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if len(usageSnapshot.Users) != 1 || usageSnapshot.Users[0].UploadBytes != 0 || usageSnapshot.Users[0].DownloadBytes != 0 {
		t.Fatalf("traffic reset was not visible immediately: %#v", usageSnapshot)
	}
}

func TestWebAPIRejectsOversizedAndUnknownRequests(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})

	oversized := httptest.NewRecorder()
	handler.ServeHTTP(oversized, httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(bytes.Repeat([]byte("x"), webAPIMaximumBodyBytes+1))))
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", oversized.Code, oversized.Body.String())
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/v1/not-a-real-resource", nil))
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "not_found") {
		t.Fatalf("unknown status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestWebAPIClusterAndRoutesUseSanitizedStructuredModels(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active"}}
	handler := mustWebAPIHandler(t, root, controller)
	api := handler.(*webAPI)
	api.syncCredentials = func(*admin.Manager, string, []string) error { return nil }

	state, err := api.manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = api.manager.UpsertClusterNode(cluster.Node{
		ID: "edge-nl", Name: "Netherlands", Region: "NL", Roles: []cluster.NodeRole{cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: "nl.example.com", PublicAddresses: []string{"8.8.4.4"}, NP2Endpoint: "127.0.0.1:1",
		HTTPSPath: "/private_https_route_0123456789", WebRTCPath: "/private_webrtc_route_0123456789",
		HTTP3Path: "/private_http3_route_01234567890", Enabled: true, ClientVisible: false,
		CredentialID: "super-secret-peer-id", HostKeySHA256: "SHA256:private-host-key", ProvisionedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.ClusterID == "" {
		t.Fatal("cluster was not initialized")
	}

	clusterResponse := httptest.NewRecorder()
	handler.ServeHTTP(clusterResponse, httptest.NewRequest(http.MethodGet, "/v1/cluster", nil))
	if clusterResponse.Code != http.StatusOK {
		t.Fatalf("cluster status=%d body=%s", clusterResponse.Code, clusterResponse.Body.String())
	}
	for _, forbidden := range []string{"private_https_route", "super-secret-peer-id", "private-host-key"} {
		if strings.Contains(clusterResponse.Body.String(), forbidden) {
			t.Fatalf("cluster response leaked %q: %s", forbidden, clusterResponse.Body.String())
		}
	}

	createdUser := httptest.NewRecorder()
	handler.ServeHTTP(createdUser, jsonRequest(t, http.MethodPost, "/v1/users", map[string]any{"name": "Route User", "profile": "web"}))
	var created struct {
		User admin.User `json:"user"`
	}
	if err := json.Unmarshal(createdUser.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	routeResponse := httptest.NewRecorder()
	handler.ServeHTTP(routeResponse, jsonRequest(t, http.MethodPost, "/v1/routes", map[string]any{
		"id": "openai-nl", "name": "OpenAI via NL", "priority": 100,
		"match":    map[string]any{"kind": "domain", "value": "openai.com"},
		"action":   map[string]any{"kind": "node", "node_ids": []string{"edge-nl"}},
		"user_ids": []string{created.User.ID},
	}))
	if routeResponse.Code != http.StatusCreated {
		t.Fatalf("route create status=%d body=%s", routeResponse.Code, routeResponse.Body.String())
	}

	routes := httptest.NewRecorder()
	handler.ServeHTTP(routes, httptest.NewRequest(http.MethodGet, "/v1/routes", nil))
	if routes.Code != http.StatusOK || !strings.Contains(routes.Body.String(), "openai-nl") || !strings.Contains(routes.Body.String(), created.User.ID) {
		t.Fatalf("routes status=%d body=%s", routes.Code, routes.Body.String())
	}
}

func TestWebAPIRoutesUsesArraysForEmptyClusterCollections(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})

	routes := httptest.NewRecorder()
	handler.ServeHTTP(routes, httptest.NewRequest(http.MethodGet, "/v1/routes", nil))
	if routes.Code != http.StatusOK {
		t.Fatalf("routes status=%d body=%s", routes.Code, routes.Body.String())
	}
	var body struct {
		Routes json.RawMessage `json:"routes"`
		Access json.RawMessage `json:"access"`
	}
	if err := json.Unmarshal(routes.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body.Routes) != "[]" || string(body.Access) != "[]" {
		t.Fatalf("empty collections must be arrays: %s", routes.Body.String())
	}
}

func TestWebAPIServicesSettingsLogsAndBackups(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &webAPITestController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active", Web: "active"}, logs: "sanitized event\n"}
	handler := mustWebAPIHandler(t, root, controller)

	services := httptest.NewRecorder()
	handler.ServeHTTP(services, httptest.NewRequest(http.MethodGet, "/v1/services", nil))
	if services.Code != http.StatusOK || !strings.Contains(services.Body.String(), `"np2":"active"`) {
		t.Fatalf("services status=%d body=%s", services.Code, services.Body.String())
	}

	restarted := httptest.NewRecorder()
	handler.ServeHTTP(restarted, jsonRequest(t, http.MethodPost, "/v1/services/restart", map[string]any{}))
	if restarted.Code != http.StatusOK || len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("restart status=%d actions=%v body=%s", restarted.Code, controller.actions, restarted.Body.String())
	}

	logs := httptest.NewRecorder()
	handler.ServeHTTP(logs, httptest.NewRequest(http.MethodGet, "/v1/logs", nil))
	if logs.Code != http.StatusOK || !strings.Contains(logs.Body.String(), "sanitized event") {
		t.Fatalf("logs status=%d body=%s", logs.Code, logs.Body.String())
	}

	settings := httptest.NewRecorder()
	handler.ServeHTTP(settings, httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), "vpn.example.com") || strings.Contains(settings.Body.String(), "private_https_route") {
		t.Fatalf("settings status=%d body=%s", settings.Code, settings.Body.String())
	}

	compatibility := httptest.NewRecorder()
	handler.ServeHTTP(compatibility, jsonRequest(t, http.MethodPost, "/v1/settings/policy", map[string]any{"mode": "compatibility", "confirm": "COMPATIBILITY"}))
	if compatibility.Code != http.StatusOK {
		t.Fatalf("policy status=%d body=%s", compatibility.Code, compatibility.Body.String())
	}

	createdBackup := httptest.NewRecorder()
	handler.ServeHTTP(createdBackup, jsonRequest(t, http.MethodPost, "/v1/backups", map[string]any{}))
	if createdBackup.Code != http.StatusCreated {
		t.Fatalf("backup status=%d body=%s", createdBackup.Code, createdBackup.Body.String())
	}
	backups := httptest.NewRecorder()
	handler.ServeHTTP(backups, httptest.NewRequest(http.MethodGet, "/v1/backups", nil))
	var backupList struct {
		Backups []map[string]string `json:"backups"`
	}
	if err := json.Unmarshal(backups.Body.Bytes(), &backupList); err != nil {
		t.Fatal(err)
	}
	if backups.Code != http.StatusOK || len(backupList.Backups) < 2 {
		t.Fatalf("backups status=%d body=%s", backups.Code, backups.Body.String())
	}
}

func TestWebAPIDoctorReturnsItsReportWhenChecksFail(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &webAPITestController{
		snapshot:      serviceSnapshot{NP2: "active", Ingress: "active", Web: "active"},
		validateError: errors.New("invalid test configuration"),
	}
	handler := mustWebAPIHandler(t, root, controller)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, jsonRequest(t, http.MethodPost, "/v1/doctor", map[string]any{}))
	if response.Code != http.StatusOK {
		t.Fatalf("doctor status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"ok":false`) || !strings.Contains(response.Body.String(), `"lines"`) {
		t.Fatalf("doctor did not preserve failed report: %s", response.Body.String())
	}
}

type webAPITestController struct {
	actions       []string
	snapshot      serviceSnapshot
	logs          string
	validateError error
}

func (controller *webAPITestController) Action(action string, _, _ io.Writer) error {
	controller.actions = append(controller.actions, action)
	return nil
}
func (controller *webAPITestController) Logs(_ bool, stdout, _ io.Writer) error {
	_, _ = io.WriteString(stdout, controller.logs)
	return nil
}
func (controller *webAPITestController) Snapshot() serviceSnapshot { return controller.snapshot }
func (controller *webAPITestController) Validate(stdout, _ io.Writer) error {
	_, _ = io.WriteString(stdout, "validated\n")
	return controller.validateError
}
func (controller *webAPITestController) PublicProbe(admin.Installation) error { return nil }
func (controller *webAPITestController) ProvisionCertificate(string, io.Writer, io.Writer) error {
	return nil
}

func mustWebAPIHandler(t *testing.T, root string, controller serviceController) http.Handler {
	t.Helper()
	handler, err := newWebAPIHandler(root, controller)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func jsonRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	return request
}
