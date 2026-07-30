package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
)

func TestWebAPIJobStoreRejectsWorkWhenCapacityIsExhausted(t *testing.T) {
	mutation := &sync.Mutex{}
	store := newWebAPIJobStore(mutation)
	release := make(chan struct{})
	for range webAPIMaximumJobs {
		if _, err := store.start("bounded", func(func(int, string)) (any, error) {
			<-release
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.start("overflow", func(func(int, string)) (any, error) { return nil, nil }); !errors.Is(err, errWebAPIJobCapacity) {
		t.Fatalf("expected capacity error, got %v", err)
	}
	close(release)
}

func TestWebAPIClusterNodeAndRouteActionsAreExplicit(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	api := handler.(*webAPI)
	api.syncCredentials = func(*admin.Manager, string, []string) error { return nil }
	if _, err := api.manager.EnsureLocalCluster(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := api.manager.UpsertClusterNode(cluster.Node{
		ID: "edge-nl", Name: "NL", Region: "NL", Roles: []cluster.NodeRole{cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: "nl.example.com", PublicAddresses: []string{"8.8.4.4"}, NP2Endpoint: "127.0.0.1:1",
		HTTPSPath: "/private_https_route_0123456789", WebRTCPath: "/private_webrtc_route_0123456789",
		HTTP3Path: "/private_http3_route_01234567890", Enabled: true, CredentialID: "peer-credential",
		HostKeySHA256: "SHA256:test", ProvisionedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	createdUser := httptest.NewRecorder()
	handler.ServeHTTP(createdUser, jsonRequest(t, http.MethodPost, "/v1/users", map[string]any{"name": "Cluster User", "profile": "web"}))
	var creation struct {
		User admin.User `json:"user"`
	}
	if err := json.Unmarshal(createdUser.Body.Bytes(), &creation); err != nil {
		t.Fatal(err)
	}

	assignNode := httptest.NewRecorder()
	handler.ServeHTTP(assignNode, jsonRequest(t, http.MethodPost, "/v1/cluster/nodes/edge-nl/assign-user", map[string]any{
		"user_id": creation.User.ID, "enabled": true,
	}))
	if assignNode.Code != http.StatusOK {
		t.Fatalf("assign node status=%d body=%s", assignNode.Code, assignNode.Body.String())
	}

	hideNode := httptest.NewRecorder()
	handler.ServeHTTP(hideNode, jsonRequest(t, http.MethodPost, "/v1/cluster/nodes/edge-nl/publish", map[string]any{"enabled": false}))
	if hideNode.Code != http.StatusOK {
		t.Fatalf("hide node status=%d body=%s", hideNode.Code, hideNode.Body.String())
	}

	drainNode := httptest.NewRecorder()
	handler.ServeHTTP(drainNode, jsonRequest(t, http.MethodPost, "/v1/cluster/nodes/edge-nl/enable", map[string]any{"enabled": false}))
	if drainNode.Code != http.StatusOK {
		t.Fatalf("drain node status=%d body=%s", drainNode.Code, drainNode.Body.String())
	}

	createRoute := httptest.NewRecorder()
	handler.ServeHTTP(createRoute, jsonRequest(t, http.MethodPost, "/v1/routes", map[string]any{
		"id": "blocked", "name": "Blocked", "priority": 10,
		"match":    map[string]any{"kind": "domain", "value": "blocked.example"},
		"action":   map[string]any{"kind": "block", "node_ids": []string{}},
		"user_ids": []string{creation.User.ID},
	}))
	if createRoute.Code != http.StatusCreated {
		t.Fatalf("create route status=%d body=%s", createRoute.Code, createRoute.Body.String())
	}

	disableRoute := httptest.NewRecorder()
	handler.ServeHTTP(disableRoute, jsonRequest(t, http.MethodPost, "/v1/routes/blocked/enable", map[string]any{"enabled": false}))
	if disableRoute.Code != http.StatusOK {
		t.Fatalf("disable route status=%d body=%s", disableRoute.Code, disableRoute.Body.String())
	}

	unassignRoute := httptest.NewRecorder()
	handler.ServeHTTP(unassignRoute, jsonRequest(t, http.MethodPost, "/v1/routes/blocked/assign-user", map[string]any{
		"user_id": creation.User.ID, "enabled": false,
	}))
	if unassignRoute.Code != http.StatusOK {
		t.Fatalf("unassign route status=%d body=%s", unassignRoute.Code, unassignRoute.Body.String())
	}

	deleteWithoutConfirmation := httptest.NewRecorder()
	handler.ServeHTTP(deleteWithoutConfirmation, jsonRequest(t, http.MethodDelete, "/v1/routes/blocked", map[string]any{}))
	if deleteWithoutConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("delete without confirmation status=%d body=%s", deleteWithoutConfirmation.Code, deleteWithoutConfirmation.Body.String())
	}

	deleteRoute := httptest.NewRecorder()
	handler.ServeHTTP(deleteRoute, jsonRequest(t, http.MethodDelete, "/v1/routes/blocked", map[string]any{"confirm": "DELETE"}))
	if deleteRoute.Code != http.StatusOK {
		t.Fatalf("delete route status=%d body=%s", deleteRoute.Code, deleteRoute.Body.String())
	}
}

func TestWebAPIHostKeyDiscoveryUsesBoundedBackgroundJobWithoutSecretEcho(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	api := handler.(*webAPI)
	api.discoverHostKey = func(draft clusterEnrollmentDraft) (string, error) {
		if string(draft.password) != "temporary-password" || draft.host != "203.0.113.20" {
			t.Fatalf("unexpected enrolment draft: host=%q password-length=%d", draft.host, len(draft.password))
		}
		return "SHA256:trusted-fingerprint", nil
	}

	started := httptest.NewRecorder()
	handler.ServeHTTP(started, jsonRequest(t, http.MethodPost, "/v1/cluster/host-key", map[string]any{
		"host": "203.0.113.20", "port": 22, "user": "root", "password": "temporary-password",
	}))
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}
	if strings.Contains(started.Body.String(), "temporary-password") {
		t.Fatalf("job start leaked password: %s", started.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("job response=%s err=%v", started.Body.String(), err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		status := httptest.NewRecorder()
		handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID, nil))
		if status.Code != http.StatusOK {
			t.Fatalf("job status=%d body=%s", status.Code, status.Body.String())
		}
		if strings.Contains(status.Body.String(), "temporary-password") {
			t.Fatalf("job status leaked password: %s", status.Body.String())
		}
		if strings.Contains(status.Body.String(), `"state":"succeeded"`) {
			if !strings.Contains(status.Body.String(), "SHA256:trusted-fingerprint") {
				t.Fatalf("job result=%s", status.Body.String())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebAPIHostKeyFailureRedactsTheTemporaryPassword(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	handler := mustWebAPIHandler(t, root, &fakeController{})
	api := handler.(*webAPI)
	api.discoverHostKey = func(clusterEnrollmentDraft) (string, error) {
		return "", errors.New("ssh rejected temporary-password")
	}
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, jsonRequest(t, http.MethodPost, "/v1/cluster/host-key", map[string]any{
		"host": "203.0.113.20", "port": 22, "user": "root", "password": "temporary-password",
	}))
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := httptest.NewRecorder()
		handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID, nil))
		if strings.Contains(status.Body.String(), "temporary-password") {
			t.Fatalf("failed job leaked password: %s", status.Body.String())
		}
		if strings.Contains(status.Body.String(), `"state":"failed"`) {
			if !strings.Contains(status.Body.String(), "[redacted]") {
				t.Fatalf("failed job did not contain redaction marker: %s", status.Body.String())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not fail: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
