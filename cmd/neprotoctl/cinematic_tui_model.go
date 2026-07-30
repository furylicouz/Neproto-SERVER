package main

import (
	"errors"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/geodata"
)

type constellationTUIAction struct {
	short string
	label string
}

var constellationTUIActions = []constellationTUIAction{
	{short: "STATUS", label: "Status and diagnostics"},
	{short: "USERS", label: "Users and client QR"},
	{short: "CLUSTER", label: "Cluster nodes and health"},
	{short: "ROUTES", label: "Traffic route policy"},
	{short: "SERVICE", label: "Service control and logs"},
	{short: "DOMAIN", label: "Domain and server settings"},
	{short: "BACKUP", label: "Backups and recovery"},
	{short: "FILES", label: "NeProto safe storage"},
	{short: "EVENTS", label: "Recent service events"},
	{short: "MAP", label: "MapSCII network map"},
	{short: "QUIT", label: "Quit"},
}

type tuiView uint8

const (
	tuiViewDashboard tuiView = iota
	tuiViewStatus
	tuiViewUsers
	tuiViewCluster
	tuiViewRoutes
	tuiViewService
	tuiViewDomain
	tuiViewBackups
	tuiViewFiles
	tuiViewEvents
	tuiViewMap
)

type tuiMapState struct {
	centerLat float64
	centerLon float64
	zoom      float64
}

type tuiDialogKind uint8

const (
	tuiDialogText tuiDialogKind = iota
	tuiDialogConfirm
	tuiDialogInfo
	tuiDialogProgress
	tuiDialogSelect
	tuiDialogMultiSelect
)

type tuiOperation uint8

const (
	tuiOperationNone tuiOperation = iota
	tuiOperationUserAdd
	tuiOperationUserExport
	tuiOperationUserRotate
	tuiOperationUserRevoke
	tuiOperationUserDelete
	tuiOperationServiceStart
	tuiOperationServiceStop
	tuiOperationServiceRestart
	tuiOperationServiceValidate
	tuiOperationServiceLogs
	tuiOperationDomainSet
	tuiOperationFeatureProduction
	tuiOperationFeatureCompatibility
	tuiOperationBackupCreate
	tuiOperationBackupRestore
	tuiOperationClusterEnrollHost
	tuiOperationClusterEnrollPort
	tuiOperationClusterEnrollUser
	tuiOperationClusterEnrollPassword
	tuiOperationClusterEnrollNodeID
	tuiOperationClusterEnrollName
	tuiOperationClusterEnrollRegion
	tuiOperationClusterEnrollDomain
	tuiOperationClusterEnrollAddresses
	tuiOperationClusterDiscoverHostKey
	tuiOperationClusterEnrollConfirm
	tuiOperationClusterToggleEnabled
	tuiOperationClusterTogglePublished
	tuiOperationClusterAssignUser
	tuiOperationClusterRemove
	tuiOperationClusterUserAccess
	tuiOperationClusterSyncUsers
	tuiOperationRouteID
	tuiOperationRouteName
	tuiOperationRoutePriority
	tuiOperationRouteMatch
	tuiOperationRouteMatchValue
	tuiOperationRouteAction
	tuiOperationRouteUsers
	tuiOperationRouteCreate
	tuiOperationRouteToggleEnabled
	tuiOperationRouteRemove
	tuiOperationRouteAssignUser
	tuiOperationGeoDataUpdate
	tuiOperationGeoDataSchedule
)

type tuiDialog struct {
	kind        tuiDialogKind
	title       string
	prompt      string
	input       string
	operation   tuiOperation
	value       string
	aux         string
	lines       []string
	scroll      int
	secret      bool
	secretRunes []rune
	progress    int
	started     time.Time
	options     []tuiDialogOption
	optionIndex int
}

type tuiDialogOption struct {
	label    string
	value    string
	selected bool
}

type tuiBackgroundEvent struct {
	operation   tuiOperation
	progress    int
	stage       string
	done        bool
	err         error
	lines       []string
	fingerprint string
	clearDraft  bool
}

type tuiBackgroundWork func(report func(int, string)) tuiBackgroundEvent

type clusterEnrollmentDraft struct {
	host        string
	port        uint16
	user        string
	password    []byte
	nodeID      string
	name        string
	region      string
	domain      string
	addresses   []string
	fingerprint string
}

type clusterRouteDraft struct {
	id       string
	name     string
	priority int
	match    string
	action   string
	userIDs  []string
}

type tuiPendingOperation struct {
	operation tuiOperation
	value     string
	aux       string
}

type constellationTUISnapshot struct {
	installation admin.Installation
	services     serviceSnapshot
	activeUsers  int
	revokedUsers int
	backups      int
}

type constellationTUIModel struct {
	now                 time.Time
	selected            int
	view                tuiView
	snapshot            constellationTUISnapshot
	host                hostMetrics
	users               []admin.User
	backups             []string
	clusterNodes        []cluster.Node
	clusterRoutes       []cluster.Route
	clusterAccess       []cluster.UserAccess
	clusterRevision     uint64
	clusterHealth       map[string]clusterNodeHealth
	geodata             geodata.UpdateStatus
	geodataSchedule     string
	output              []string
	listIndex           int
	scroll              int
	mapState            tuiMapState
	dialog              *tuiDialog
	pending             tuiPendingOperation
	backgroundOperation tuiOperation
	clusterDraft        clusterEnrollmentDraft
	routeDraft          clusterRouteDraft
	rxHistory           []uint64
	txHistory           []uint64
	rxRate              uint64
	txRate              uint64

	lastRX     uint64
	lastTX     uint64
	lastSample time.Time
	lastFull   time.Time
	status     string
}

func (m *constellationTUIModel) openSelectedView() (quit bool) {
	if m.selected < 0 || m.selected >= len(constellationTUIActions) {
		return false
	}
	action := constellationTUIActions[m.selected].short
	if action == "QUIT" {
		return true
	}
	m.view = mapTUIView(action)
	m.listIndex = 0
	m.scroll = 0
	m.output = nil
	if m.view == tuiViewMap && m.mapState.zoom < 1 {
		m.mapState.zoom = 1
	}
	return false
}

func mapTUIView(action string) tuiView {
	switch action {
	case "STATUS":
		return tuiViewStatus
	case "USERS":
		return tuiViewUsers
	case "CLUSTER":
		return tuiViewCluster
	case "ROUTES":
		return tuiViewRoutes
	case "SERVICE":
		return tuiViewService
	case "DOMAIN":
		return tuiViewDomain
	case "BACKUP":
		return tuiViewBackups
	case "FILES":
		return tuiViewFiles
	case "EVENTS":
		return tuiViewEvents
	case "MAP":
		return tuiViewMap
	default:
		return tuiViewDashboard
	}
}

func (m *constellationTUIModel) moveSelection(delta int) {
	if len(constellationTUIActions) == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta) % len(constellationTUIActions)
	if m.selected < 0 {
		m.selected += len(constellationTUIActions)
	}
}

func (m *constellationTUIModel) refresh(
	manager *admin.Manager,
	controller serviceController,
	now time.Time,
	forceFull bool,
) error {
	m.now = now
	host := collectLinuxHostMetrics()
	if !m.lastSample.IsZero() && now.After(m.lastSample) {
		seconds := now.Sub(m.lastSample).Seconds()
		if host.NetworkRXBytes >= m.lastRX {
			m.rxRate = uint64(float64(host.NetworkRXBytes-m.lastRX) / seconds)
		}
		if host.NetworkTXBytes >= m.lastTX {
			m.txRate = uint64(float64(host.NetworkTXBytes-m.lastTX) / seconds)
		}
		m.rxHistory = appendTUIHistory(m.rxHistory, m.rxRate, 48)
		m.txHistory = appendTUIHistory(m.txHistory, m.txRate, 48)
	}
	m.host = host
	m.lastRX, m.lastTX, m.lastSample = host.NetworkRXBytes, host.NetworkTXBytes, now

	if !forceFull && !m.lastFull.IsZero() && now.Sub(m.lastFull) < 5*time.Second {
		return nil
	}
	users, err := manager.ListUsers()
	if err != nil {
		return err
	}
	backups, err := manager.ListBackups()
	if err != nil {
		return err
	}
	clusterState, clusterErr := manager.ClusterState()
	if clusterErr == nil {
		m.clusterNodes = append(m.clusterNodes[:0], clusterState.Nodes...)
		m.clusterRoutes = append(m.clusterRoutes[:0], clusterState.Routes...)
		m.clusterAccess = append(m.clusterAccess[:0], clusterState.Access...)
		m.clusterRevision = clusterState.Revision
		m.clusterHealth = probeClusterNodes(clusterState.Nodes, 1200*time.Millisecond)
	} else if errors.Is(clusterErr, cluster.ErrStateNotFound) {
		m.clusterNodes = nil
		m.clusterRoutes = nil
		m.clusterAccess = nil
		m.clusterRevision = 0
		m.clusterHealth = nil
	} else {
		return clusterErr
	}
	if status, statusErr := geodata.Status(manager.GeodataDirectory()); statusErr == nil {
		m.geodata = status
	} else {
		m.geodata = geodata.UpdateStatus{State: geodata.UpdateStateError, Error: statusErr.Error()}
	}
	m.geodataSchedule = geoDataSchedule(manager.RootDirectory())
	active, revoked := managedUserCounts(users)
	m.snapshot = constellationTUISnapshot{
		installation: manager.Installation(), services: controller.Snapshot(),
		activeUsers: active, revokedUsers: revoked, backups: len(backups),
	}
	m.users = append(m.users[:0], users...)
	m.backups = append(m.backups[:0], backups...)
	if m.listIndex >= len(m.users) && len(m.users) > 0 {
		m.listIndex = len(m.users) - 1
	}
	m.lastFull = now
	return nil
}

func appendTUIHistory(values []uint64, value uint64, maximum int) []uint64 {
	values = append(values, value)
	if len(values) > maximum {
		copy(values, values[len(values)-maximum:])
		values = values[:maximum]
	}
	return values
}
