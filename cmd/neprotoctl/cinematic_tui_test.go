package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
)

func TestConstellationTUIRendersEDEXStyleOperationalLayout(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 42)
	model := constellationTUIModel{
		now: time.Date(2026, 7, 19, 12, 47, 55, 0, time.UTC), selected: 1,
		snapshot: constellationTUISnapshot{
			installation: admin.Installation{
				Mode: "bare-metal", Domain: "vpn.example.com", ServerAddresses: []string{"8.8.8.8"},
				EnableConstellation: true, EnableForwardSecrecy: true,
			},
			services:    serviceSnapshot{NP2: "active", Ingress: "active"},
			activeUsers: 3, revokedUsers: 4, backups: 2,
		},
		host: hostMetrics{
			Hostname: "edge-01", Uptime: "2d 04h 35m", Load: "0.42",
			Memory: "3.0/8.0 GiB 37%", MemoryPercent: 37,
			NetworkRX: "1.2 GiB", NetworkTX: "620.0 MiB",
		},
		rxHistory: []uint64{1, 3, 7, 4, 9}, txHistory: []uint64{2, 2, 4, 8, 5},
	}

	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{
		"NP/2 CONSTELLATION", "SYSTEM MONITOR", "CONTROL TERMINAL", "NETWORK MAP",
		"vpn.example.com", "HTTPS/WSS", "HTTP/3", "WEBRTC", "WORLD LINK",
		"CONFIG", "USERS", "CERTS", "BACKUPS", "EVENTS", "edge-01",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("TUI missing %q:\n%s", expected, content)
		}
	}
}

func TestConstellationTUIDashboardShowsClusterNodeHealth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(160, 55)
	model := constellationTUIModel{
		now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		snapshot: constellationTUISnapshot{
			installation: admin.Installation{Domain: "vpn.example.com", ServerAddresses: []string{"8.8.8.8"}},
			services:     serviceSnapshot{NP2: "active", Ingress: "active"},
		},
		clusterNodes: []cluster.Node{
			{ID: "master", Name: "Primary", Enabled: true, ClientVisible: true},
			{ID: "edge-nl", Name: "n2-NL", Enabled: true, ClientVisible: true},
		},
		clusterHealth: map[string]clusterNodeHealth{
			"master":  {status: "UP", latency: 12 * time.Millisecond},
			"edge-nl": {status: "DOWN"},
		},
	}

	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"CLUSTER  1/2 UP", "Primary", "UP 12ms", "n2-NL", "DOWN"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("dashboard cluster status missing %q:\n%s", expected, content)
		}
	}
}

func TestConstellationTUIInfoDialogFitsCompleteTerminalQR(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 64)
	lines := make([]string, 53)
	for index := range lines {
		lines[index] = strings.Repeat("█", 96) + "RIGHTEDGE"
	}
	lines[0] = "QR-TOP" + strings.Repeat("█", 90) + "RIGHTEDGE"
	lines[len(lines)-1] = "QR-BOTTOM" + strings.Repeat("█", 87) + "RIGHTEDGE"
	model := constellationTUIModel{
		now:    time.Now(),
		dialog: &tuiDialog{kind: tuiDialogInfo, title: "CLIENT CREDENTIAL", lines: lines},
	}

	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"QR-TOP", "QR-BOTTOM", "RIGHTEDGE", "ENTER/ESC CLOSE"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("terminal QR dialog clipped %q:\n%s", expected, content)
		}
	}
}

func TestConstellationTUIRendersClusterAndRouteWorkspacesInsidePanel(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 42)
	model := constellationTUIModel{
		now: time.Now(), view: tuiViewCluster, clusterRevision: 7,
		clusterNodes: []cluster.Node{{ID: "edge-01", Name: "Helsinki Edge", Region: "Finland", Roles: []cluster.NodeRole{cluster.RoleEgress}, Enabled: true, ClientVisible: true}},
	}
	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"CLUSTER FABRIC", "Helsinki Edge", "Finland", "EGRESS", "REVISION 7", "A ASSIGN USER"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("cluster workspace missing %q:\n%s", expected, content)
		}
	}

	model.view = tuiViewRoutes
	model.clusterRoutes = []cluster.Route{{ID: "media", Name: "Media sites", Priority: 10, Enabled: true, Source: cluster.RouteSourceAdmin, Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}}}}
	renderConstellationTUI(screen, &model)
	content = simulationText(screen)
	for _, expected := range []string{"ROUTE POLICY", "Media sites", "NODE", "edge-01", "N NEW"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("route workspace missing %q:\n%s", expected, content)
		}
	}
}

func TestClusterPanelAssignsAndRemovesOneNodeForOneUser(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Selected edge user", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, node := range []cluster.Node{
		{ID: "edge-fi", Name: "Finland", Region: "Helsinki", Roles: []cluster.NodeRole{cluster.RoleEgress}, PublicIdentity: "fi.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "fi.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "peer-fi", HostKeySHA256: "SHA256:fi", ProvisionedAt: now, UpdatedAt: now},
		{ID: "edge-de", Name: "Germany", Region: "Frankfurt", Roles: []cluster.NodeRole{cluster.RoleEgress}, PublicIdentity: "de.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "de.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "peer-de", HostKeySHA256: "SHA256:de", ProvisionedAt: now, UpdatedAt: now},
	} {
		if _, err := manager.UpsertClusterNode(node); err != nil {
			t.Fatal(err)
		}
	}
	var synchronized [][]string
	syncer := func(_ *admin.Manager, gotUser string, nodes []string) error {
		if gotUser != user.ID {
			t.Fatalf("sync user=%q want=%q", gotUser, user.ID)
		}
		synchronized = append(synchronized, append([]string(nil), nodes...))
		return nil
	}
	enabled, err := toggleClusterNodeForUserSynced(manager, "edge-fi", user.ID, syncer)
	if err != nil || !enabled {
		t.Fatalf("assign node enabled=%t err=%v", enabled, err)
	}
	catalog, _, err := manager.SignedClusterCatalog(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Servers) != 2 || catalog.Servers[0].NodeID != state.Nodes[0].ID || catalog.Servers[1].NodeID != "edge-fi" {
		t.Fatalf("selected-node catalog=%+v", catalog.Servers)
	}
	enabled, err = toggleClusterNodeForUserSynced(manager, "edge-fi", user.ID, syncer)
	if err != nil || enabled {
		t.Fatalf("remove node enabled=%t err=%v", enabled, err)
	}
	catalog, _, err = manager.SignedClusterCatalog(user.ID, time.Hour)
	if err != nil || len(catalog.Servers) != 1 || catalog.Servers[0].NodeID != state.Nodes[0].ID {
		t.Fatalf("master-only catalog=%+v err=%v", catalog.Servers, err)
	}
	if len(synchronized) != 2 || !containsString(synchronized[0], "edge-fi") || containsString(synchronized[0], "edge-de") || containsString(synchronized[1], "edge-fi") {
		t.Fatalf("credential synchronization=%v", synchronized)
	}
}

func TestClusterNodeAssignmentDoesNotPublishAfterCredentialSyncFailure(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Atomic node user", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	masterID := state.Nodes[0].ID
	if _, err := manager.SetClusterUserAccess(cluster.UserAccess{UserID: user.ID, AllowedNodeIDs: []string{masterID}, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := manager.UpsertClusterNode(cluster.Node{
		ID: "edge-fi", Name: "Finland", Region: "Helsinki", Roles: []cluster.NodeRole{cluster.RoleEgress},
		PublicIdentity: "fi.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "fi.example.com:443",
		Enabled: true, ClientVisible: true, CredentialID: "peer-fi", HostKeySHA256: "SHA256:fi", ProvisionedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	syncer := func(_ *admin.Manager, _ string, nodes []string) error {
		calls++
		if calls == 1 {
			return errors.New("edge unavailable")
		}
		if len(nodes) != 1 || nodes[0] != masterID {
			t.Fatalf("rollback nodes=%v want=%q", nodes, masterID)
		}
		return nil
	}
	if enabled, err := toggleClusterNodeForUserSynced(manager, "edge-fi", user.ID, syncer); err == nil || enabled || calls != 2 {
		t.Fatalf("enabled=%t calls=%d err=%v", enabled, calls, err)
	}
	state, err = manager.ClusterState()
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range state.Access {
		if access.UserID == user.ID && containsString(access.AllowedNodeIDs, "edge-fi") {
			t.Fatalf("failed pre-sync published node access: %+v", access)
		}
	}
}

func TestConstellationTUIMapsClusterAndRoutesWithoutLeavingMainScreen(t *testing.T) {
	if mapTUIView("CLUSTER") != tuiViewCluster || mapTUIView("ROUTES") != tuiViewRoutes {
		t.Fatal("cluster workspaces are not mapped")
	}
}

func TestConstellationTUINavigationWrapsAndCompactLayoutRemainsUsable(t *testing.T) {
	model := constellationTUIModel{selected: 0}
	model.moveSelection(-1)
	if model.selected != len(constellationTUIActions)-1 {
		t.Fatalf("wrapped selection=%d", model.selected)
	}
	model.moveSelection(1)
	if model.selected != 0 {
		t.Fatalf("forward wrapped selection=%d", model.selected)
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(78, 22)
	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	if !strings.Contains(content, "NP/2 CONSTELLATION") || !strings.Contains(content, "Resize terminal") ||
		!strings.Contains(content, "Quit") {
		t.Fatalf("compact layout is not usable:\n%s", content)
	}
}

func TestConstellationTUIKeyboardShortcutsSelectRealActions(t *testing.T) {
	model := constellationTUIModel{selected: 3, view: tuiViewDashboard}
	quit, selected := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyF2, 0, tcell.ModNone), &model)
	if quit || !selected || model.selected != 1 || model.view != tuiViewUsers {
		t.Fatalf("F2 result: quit=%v selected=%v index=%d view=%v", quit, selected, model.selected, model.view)
	}
	quit, selected = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone), &model)
	if quit || selected || model.view != tuiViewDashboard {
		t.Fatalf("q in workspace result: quit=%v selected=%v view=%v", quit, selected, model.view)
	}
}

func TestConstellationTUIOpensWorkspacesWithoutLeavingScreen(t *testing.T) {
	model := constellationTUIModel{selected: 1, view: tuiViewDashboard}
	quit, selected := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if quit || !selected || model.view != tuiViewUsers {
		t.Fatalf("open users: quit=%v selected=%v view=%v", quit, selected, model.view)
	}
	quit, selected = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), &model)
	if quit || selected || model.view != tuiViewDashboard {
		t.Fatalf("back to dashboard: quit=%v selected=%v view=%v", quit, selected, model.view)
	}
}

func TestConstellationTUIRendersUserWorkspaceInsideMainFrame(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 42)
	model := constellationTUIModel{
		now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC), view: tuiViewUsers,
		users: []admin.User{{ID: "user-id", Name: "Fury iPhone", Profile: "web", Status: admin.StatusActive}},
	}
	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"SYSTEM MONITOR", "USER ACCESS MATRIX", "Fury iPhone", "N NEW", "ESC BACK", "NETWORK MAP"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("users workspace missing %q:\n%s", expected, content)
		}
	}
}

func TestBrailleWorldMapIsOfflineBoundedAndMarksGateway(t *testing.T) {
	lines := renderBrailleWorldMap(42, 12, tuiMapState{zoom: 1})
	if len(lines) != 12 {
		t.Fatalf("height=%d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "◆") {
		t.Fatalf("gateway marker missing:\n%s", joined)
	}
	hasBraille := false
	for _, character := range joined {
		if character >= '\u2801' && character <= '\u28ff' {
			hasBraille = true
			break
		}
	}
	if !hasBraille {
		t.Fatalf("braille coastline missing:\n%s", joined)
	}
}

func TestConstellationTUIWorkspaceActionsStayInsideDialog(t *testing.T) {
	model := constellationTUIModel{
		view:  tuiViewUsers,
		users: []admin.User{{ID: "user-1", Name: "Alice", Profile: "web", Status: admin.StatusActive}},
	}
	quit, invoke := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), &model)
	if quit || invoke || model.dialog == nil || model.dialog.operation != tuiOperationUserAdd {
		t.Fatalf("new-user dialog not opened: quit=%v invoke=%v model=%+v", quit, invoke, model)
	}
	for _, character := range "Bob" {
		handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), &model)
	}
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), &model)
	quit, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if quit || !invoke || model.pending.operation != tuiOperationUserAdd || model.pending.value != "Bob" || model.pending.aux != "web" {
		t.Fatalf("new-user action not queued internally: quit=%v invoke=%v pending=%+v", quit, invoke, model.pending)
	}

	model.view = tuiViewService
	quit, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone), &model)
	if quit || invoke || model.dialog == nil || model.dialog.operation != tuiOperationServiceStop {
		t.Fatalf("stop confirmation not opened: quit=%v invoke=%v dialog=%+v", quit, invoke, model.dialog)
	}

	model.dialog = nil
	model.view = tuiViewCluster
	model.clusterNodes = []cluster.Node{{ID: "edge-fi", Name: "Finland", Enabled: true}}
	quit, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), &model)
	if quit || invoke || model.dialog == nil || model.dialog.operation != tuiOperationClusterAssignUser || model.dialog.aux != "edge-fi" {
		t.Fatalf("node assignment dialog not opened: quit=%v invoke=%v dialog=%+v", quit, invoke, model.dialog)
	}
}

func TestConstellationTUISeparatesRevokeDeleteAndShowsManualExport(t *testing.T) {
	model := constellationTUIModel{
		view:  tuiViewUsers,
		users: []admin.User{{ID: "user-1", Name: "Alice", Profile: "web", Status: admin.StatusActive}},
	}
	_, invoke := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone), &model)
	if invoke || model.dialog == nil || model.dialog.kind != tuiDialogInfo || !strings.Contains(strings.Join(model.dialog.lines, " "), "revoke") {
		t.Fatalf("active delete must explain revoke-first policy: %+v", model.dialog)
	}
	model.dialog = nil
	_, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), &model)
	if invoke || model.dialog == nil || model.dialog.operation != tuiOperationUserRevoke {
		t.Fatalf("revoke action=%+v invoke=%t", model.dialog, invoke)
	}
	model.dialog = nil
	model.users[0].Status = admin.StatusRevoked
	_, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone), &model)
	if invoke || model.dialog == nil || model.dialog.operation != tuiOperationUserDelete {
		t.Fatalf("delete action=%+v invoke=%t", model.dialog, invoke)
	}

	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Manual settings", "web")
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	model = constellationTUIModel{view: tuiViewUsers, pending: tuiPendingOperation{operation: tuiOperationUserExport, value: user.ID}}
	executeConstellationOperation(manager, controller, &model)
	joined := strings.Join(model.dialog.lines, "\n")
	for _, expected := range []string{"MANUAL SETTINGS", "Server: vpn.example.com", "Credential ID: " + user.ID, "Secret:", "Import URI:", "CLIENT QR"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("export missing %q:\n%s", expected, joined)
		}
	}
}

func TestConstellationTUIRouteWizardUsesChoicesForMatchNodeAndUsers(t *testing.T) {
	model := constellationTUIModel{
		view: tuiViewRoutes,
		users: []admin.User{
			{ID: "alice", Name: "Alice iPhone", Status: admin.StatusActive},
			{ID: "revoked", Name: "Old device", Status: admin.StatusRevoked},
		},
		clusterNodes: []cluster.Node{
			{ID: "master", Name: "Moscow", Region: "RU", Enabled: true},
			{ID: "edge-nl", Name: "Amsterdam", Region: "NL", Enabled: true},
		},
	}
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), &model)
	enterDialogValue(t, &model, "youtube-nl")
	enterDialogValue(t, &model, "YouTube via NL")
	enterDialogValue(t, &model, "10")
	if model.dialog == nil || model.dialog.kind != tuiDialogSelect || model.dialog.operation != tuiOperationRouteMatch {
		t.Fatalf("match kind selector=%+v", model.dialog)
	}
	selectDialogValue(t, &model, "geosite")
	enterDialogValue(t, &model, "youtube")
	if model.dialog == nil || model.dialog.kind != tuiDialogSelect || model.dialog.operation != tuiOperationRouteAction {
		t.Fatalf("action selector=%+v", model.dialog)
	}
	selectDialogValue(t, &model, "node:edge-nl")
	if model.dialog == nil || model.dialog.kind != tuiDialogMultiSelect || model.dialog.operation != tuiOperationRouteUsers {
		t.Fatalf("user selector=%+v", model.dialog)
	}
	if len(model.dialog.options) != 1 || model.dialog.options[0].value != "alice" {
		t.Fatalf("user options=%+v", model.dialog.options)
	}
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), &model)
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if model.dialog == nil || model.dialog.kind != tuiDialogConfirm || model.dialog.operation != tuiOperationRouteCreate {
		t.Fatalf("route confirmation=%+v", model.dialog)
	}
	_, invoke := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if !invoke || model.pending.operation != tuiOperationRouteCreate || model.routeDraft.match != "geosite:youtube" ||
		model.routeDraft.action != "node:edge-nl" || len(model.routeDraft.userIDs) != 1 || model.routeDraft.userIDs[0] != "alice" {
		t.Fatalf("pending=%+v draft=%+v", model.pending, model.routeDraft)
	}
}

func enterDialogValue(t *testing.T, model *constellationTUIModel, value string) {
	t.Helper()
	if model.dialog == nil || model.dialog.kind != tuiDialogText {
		t.Fatalf("text dialog expected, got %+v", model.dialog)
	}
	model.dialog.input = value
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), model)
}

func selectDialogValue(t *testing.T, model *constellationTUIModel, value string) {
	t.Helper()
	if model.dialog == nil || model.dialog.kind != tuiDialogSelect {
		t.Fatalf("select dialog expected, got %+v", model.dialog)
	}
	for index, option := range model.dialog.options {
		if option.value == value {
			model.dialog.optionIndex = index
			handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), model)
			return
		}
	}
	t.Fatalf("option %q not found in %+v", value, model.dialog.options)
}

func TestConstellationTUICollectsClusterEnrollmentWithoutExposingPassword(t *testing.T) {
	model := constellationTUIModel{view: tuiViewCluster}
	_, invoke := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), &model)
	if invoke || model.dialog == nil || model.dialog.operation != tuiOperationClusterEnrollHost {
		t.Fatalf("cluster enrollment did not start: %+v", model.dialog)
	}
	values := []struct {
		value string
		next  tuiOperation
	}{
		{"203.0.113.50", tuiOperationClusterEnrollPort},
		{"22", tuiOperationClusterEnrollUser},
		{"root", tuiOperationClusterEnrollPassword},
		{"temporary-password", tuiOperationClusterEnrollNodeID},
		{"edge-fi", tuiOperationClusterEnrollName},
		{"Finland Edge", tuiOperationClusterEnrollRegion},
		{"Helsinki", tuiOperationClusterEnrollDomain},
		{"edge.example.com", tuiOperationClusterEnrollAddresses},
	}
	for _, step := range values {
		model.dialog.input = ""
		for _, character := range step.value {
			handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), &model)
		}
		_, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
		if invoke || model.dialog == nil || model.dialog.operation != step.next {
			t.Fatalf("after %q next dialog=%+v invoke=%v", step.value, model.dialog, invoke)
		}
	}
	for _, character := range "1.1.1.1" {
		handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), &model)
	}
	_, invoke = handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), &model)
	if !invoke || model.pending.operation != tuiOperationClusterDiscoverHostKey || string(model.clusterDraft.password) != "temporary-password" {
		t.Fatalf("discovery not queued safely: pending=%+v draft=%+v", model.pending, model.clusterDraft)
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	model.dialog = &tuiDialog{kind: tuiDialogText, title: "PASSWORD", prompt: "SSH PASSWORD", operation: tuiOperationClusterEnrollPassword, secret: true, secretRunes: []rune("do-not-render")}
	renderConstellationTUI(screen, &model)
	if content := simulationText(screen); strings.Contains(content, "do-not-render") || !strings.Contains(content, "*************") {
		t.Fatalf("password masking failed:\n%s", content)
	}
}

func TestConstellationTUIDialogIsRenderedOverWorkspace(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(132, 42)
	model := constellationTUIModel{now: time.Now(), view: tuiViewDomain, dialog: &tuiDialog{kind: tuiDialogText, title: "CHANGE DOMAIN", prompt: "PUBLIC DNS NAME", input: "vpn.example.com", operation: tuiOperationDomainSet}}
	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"CHANGE DOMAIN", "PUBLIC DNS NAME", "vpn.example.com", "ENTER APPLY", "ESC CANCEL"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("dialog missing %q:\n%s", expected, content)
		}
	}
}

func TestConstellationTUIExecutesUserAndServiceOperationsWithoutSuspending(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	model := constellationTUIModel{
		now:     time.Now(),
		view:    tuiViewUsers,
		pending: tuiPendingOperation{operation: tuiOperationUserAdd, value: "Test iPhone", aux: "web"},
	}
	executeConstellationOperation(manager, controller, &model)
	users, err := manager.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "Test iPhone" || model.dialog == nil || model.dialog.kind != tuiDialogInfo {
		t.Fatalf("user operation did not remain in result dialog: users=%+v dialog=%+v", users, model.dialog)
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("user restart actions=%v", controller.actions)
	}

	model.dialog = nil
	model.pending.operation = tuiOperationServiceStop
	executeConstellationOperation(manager, controller, &model)
	if len(controller.actions) != 2 || controller.actions[1] != "stop" || model.dialog == nil {
		t.Fatalf("service operation actions=%v dialog=%+v", controller.actions, model.dialog)
	}
}

func TestClusterPanelCreatesRoutesAndPublishesPerUserCatalog(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Cluster iPhone", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	edge := cluster.Node{
		ID: "edge-fi", Name: "Finland", Region: "Helsinki", Roles: []cluster.NodeRole{cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: "edge.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "edge.example.com:443",
		Enabled: true, ClientVisible: true, CredentialID: "peer-edge", HostKeySHA256: "SHA256:test", ProvisionedAt: now, UpdatedAt: now,
	}
	if _, err := manager.UpsertClusterNode(edge); err != nil {
		t.Fatal(err)
	}
	draft := clusterRouteDraft{id: "media", name: "Media", priority: 10, match: "domain:youtube.com", action: "node:edge-fi"}
	if err := createClusterRoute(manager, draft); err != nil {
		t.Fatal(err)
	}
	if enabled, err := toggleUserClusterAccess(manager, user.ID); err != nil || !enabled {
		t.Fatalf("enable user cluster access: enabled=%v err=%v", enabled, err)
	}
	catalog, _, err := manager.SignedClusterCatalog(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Servers) != 2 || len(catalog.AdminRoutes) != 1 || catalog.AdminRoutes[0].ID != "media" || state.ClusterID == "" {
		t.Fatalf("catalog did not expose selected cluster state: %+v", catalog)
	}
	if protocols := catalog.AdminRoutes[0].Match.Protocols; len(protocols) != 2 ||
		protocols[0] != cluster.ProtocolTCP || protocols[1] != cluster.ProtocolUDP {
		t.Fatalf("new route protocols=%v, want tcp+udp", protocols)
	}
	if enabled, err := toggleUserClusterAccess(manager, user.ID); err != nil || enabled {
		t.Fatalf("revoke user cluster access: enabled=%v err=%v", enabled, err)
	}
	if _, _, err := manager.SignedClusterCatalog(user.ID, time.Hour); err == nil {
		t.Fatal("catalog remained available after access revocation")
	}
}

func TestRouteWizardAssignmentGrantsOnlyMasterAndSelectedEgress(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager := mustOpenManager(t, root)
	user, err := manager.AddUser("Selected route user", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, node := range []cluster.Node{
		{ID: "edge-nl", Name: "Netherlands", Region: "NL", Roles: []cluster.NodeRole{cluster.RoleEgress}, PublicIdentity: "nl.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "nl.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "peer-nl", HostKeySHA256: "SHA256:nl", ProvisionedAt: now, UpdatedAt: now},
		{ID: "edge-de", Name: "Germany", Region: "DE", Roles: []cluster.NodeRole{cluster.RoleEgress}, PublicIdentity: "de.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "de.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "peer-de", HostKeySHA256: "SHA256:de", ProvisionedAt: now, UpdatedAt: now},
	} {
		if _, err := manager.UpsertClusterNode(node); err != nil {
			t.Fatal(err)
		}
	}
	draft := clusterRouteDraft{
		id: "youtube-nl", name: "YouTube NL", priority: 10,
		match: "domain:youtube.com", action: "node:edge-nl", userIDs: []string{user.ID},
	}
	var synchronized []string
	if err := createClusterRouteForUsersSynced(manager, draft, func(_ *admin.Manager, gotUser string, nodes []string) error {
		if gotUser != user.ID {
			t.Fatalf("sync user=%q", gotUser)
		}
		synchronized = append([]string(nil), nodes...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	masterID := state.Nodes[0].ID
	if !containsString(synchronized, masterID) || !containsString(synchronized, "edge-nl") || containsString(synchronized, "edge-de") {
		t.Fatalf("synchronized nodes=%v", synchronized)
	}
	updated, err := manager.ClusterState()
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range updated.Access {
		if access.UserID == user.ID {
			if !containsString(access.AllowedRouteIDs, "youtube-nl") || containsString(access.AllowedNodeIDs, "edge-de") {
				t.Fatalf("route access=%+v", access)
			}
			return
		}
	}
	t.Fatal("route user access was not created")
}

func TestClusterAccessGrantDoesNotPublishCatalogWhenCredentialPreSyncFails(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Atomic cluster user", "web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureLocalCluster(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	syncer := func(_ *admin.Manager, gotUser string, nodes []string) error {
		calls++
		if gotUser != user.ID {
			t.Fatalf("sync user=%q", gotUser)
		}
		if calls == 1 {
			return errors.New("edge unavailable")
		}
		if len(nodes) != 0 {
			t.Fatalf("rollback nodes=%v", nodes)
		}
		return nil
	}
	enabled, err := toggleUserClusterAccessSynced(manager, user.ID, syncer)
	if err == nil || enabled || calls != 2 {
		t.Fatalf("enabled=%v calls=%d err=%v", enabled, calls, err)
	}
	state, err := manager.ClusterState()
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range state.Access {
		if access.UserID == user.ID {
			t.Fatalf("failed pre-sync published access: %+v", access)
		}
	}
}

func TestRouteAssignmentRollsCredentialTargetsBackBeforePublishingFailure(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Route rollback", "web")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	masterID := state.Nodes[0].ID
	if _, err := manager.SetClusterUserAccess(cluster.UserAccess{
		UserID: user.ID, AllowedNodeIDs: []string{masterID}, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := createClusterRoute(manager, clusterRouteDraft{
		id: "blocked-media", name: "Blocked media", priority: 1,
		match: "domain:example.com", action: "block",
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	syncer := func(_ *admin.Manager, _ string, nodes []string) error {
		calls++
		if calls == 1 {
			return errors.New("sync failed")
		}
		if len(nodes) != 1 || nodes[0] != masterID {
			t.Fatalf("rollback nodes=%v want=%q", nodes, masterID)
		}
		return nil
	}
	if err := assignRouteToUserSynced(manager, "blocked-media", user.ID, syncer); err == nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	state, err = manager.ClusterState()
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range state.Access {
		if access.UserID == user.ID && containsString(access.AllowedRouteIDs, "blocked-media") {
			t.Fatalf("failed route pre-sync was published: %+v", access)
		}
	}
}

func TestConstellationTUIDoesNotCapturePipedAutomationInput(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	if terminalInput(input) {
		t.Fatal("pipe was treated as an interactive terminal")
	}
	if terminalInput(strings.NewReader("0\n")) {
		t.Fatal("buffer was treated as an interactive terminal")
	}
}

func TestConstellationTUISparklineAndRateFormatting(t *testing.T) {
	if got := tuiSparkline([]uint64{0, 5, 10}, 5); got != "▁▁▁▄█" {
		t.Fatalf("sparkline=%q", got)
	}
	if got := formatTUIRate(1536); got != "1.5 KiB/s" {
		t.Fatalf("rate=%q", got)
	}
}

func simulationText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var output strings.Builder
	for y := range height {
		for x := range width {
			cell := cells[y*width+x]
			if len(cell.Runes) == 0 {
				output.WriteByte(' ')
			} else {
				output.WriteRune(cell.Runes[0])
			}
		}
		output.WriteByte('\n')
	}
	return output.String()
}
