package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterprovision"
	"neproto.local/chameleon/internal/geodata"
)

func discoverClusterEnrollmentHostKeyForDraft(draft clusterEnrollmentDraft) (string, error) {
	request := clusterprovision.EnrollmentRequest{
		Host: draft.host, Port: draft.port, User: draft.user,
		Password: append([]byte(nil), draft.password...), NodeID: draft.nodeID, Name: draft.name, Region: draft.region,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fingerprint, err := clusterprovision.DiscoverSSHHostKey(ctx, request)
	if err != nil {
		return "", err
	}
	return fingerprint, nil
}

func enrolClusterNodeDraft(
	manager *admin.Manager,
	controller serviceController,
	draft clusterEnrollmentDraft,
	output, failures io.Writer,
	report func(int, string),
) error {
	defer zeroClusterEnrollmentDraft(&draft)
	reportClusterEnrollment(report, 3, "Initializing the authoritative cluster master")
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		return fmt.Errorf("initialize cluster master: %w", err)
	}
	var master cluster.Node
	for _, node := range state.Nodes {
		if containsClusterRole(node.Roles, cluster.RoleMaster) {
			master = node
			break
		}
	}
	if master.ID == "" {
		return cluster.ErrInvalidState
	}
	reportClusterEnrollment(report, 7, "Generating isolated peer credentials")
	material, err := manager.NewClusterPeerMaterial()
	if err != nil {
		return err
	}
	paths, err := newClusterTransportPaths()
	if err != nil {
		return err
	}
	bootstrap, err := clusterprovision.EncodeBootstrap(clusterprovision.Bootstrap{
		Version: clusterprovision.BootstrapVersion, Mode: manager.Installation().Mode,
		Domain: draft.domain, Addresses: append([]string(nil), draft.addresses...),
		HTTPSPath: paths[0], WebRTCPath: paths[1], HTTP3Path: paths[2],
		ClusterID: state.ClusterID, NodeID: draft.nodeID, Name: draft.name, Region: draft.region,
		Roles: []cluster.NodeRole{cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress}, MasterNodeID: master.ID,
		MasterDomain: master.PublicIdentity, MasterAddresses: append([]string(nil), master.PublicAddresses...),
		MasterHTTPSPath: master.HTTPSPath, MasterWebRTCPath: master.WebRTCPath, MasterHTTP3Path: master.HTTP3Path,
		PeerCredentialID: material.CredentialID, PeerSecret: base64.RawURLEncoding.EncodeToString(material.Secret[:]),
	})
	if err != nil {
		return err
	}
	bundle, err := os.Open(manager.ServerBundlePath())
	if err != nil {
		return fmt.Errorf("open retained server bundle: %w", err)
	}
	defer bundle.Close()
	request := clusterprovision.EnrollmentRequest{
		Host: draft.host, Port: draft.port, User: draft.user, Password: append([]byte(nil), draft.password...),
		PinnedHostKey: draft.fingerprint, NodeID: draft.nodeID, Name: draft.name, Region: draft.region,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	provisioner := clusterprovision.Provisioner{
		Dial: clusterprovision.DialSSH,
		Progress: func(update clusterprovision.EnrollmentProgress) {
			progress := 10 + update.Step*9
			reportClusterEnrollment(report, progress, update.Message)
		},
	}
	if _, err := provisioner.Enroll(ctx, request, bundle, bootstrap); err != nil {
		return err
	}
	reportClusterEnrollment(report, 78, "Installing peer authorization on the master")
	endpoint := admin.ClusterPeerEndpoint{
		NodeID: draft.nodeID, ServerIdentity: draft.domain, ServerAddresses: append([]string(nil), draft.addresses...),
		HTTPSPath: paths[0], WebRTCPath: paths[1], HTTP3Path: paths[2],
	}
	if err := manager.InstallClusterPeer(master.ID, endpoint, material); err != nil {
		return fmt.Errorf("remote node installed but master peer commit failed: %w", err)
	}
	reportClusterEnrollment(report, 86, "Authenticating an end-to-end NP/2 peer session")
	if err := attestProductionClusterPeer(manager, endpoint.NodeID); err != nil {
		rollbackErr := manager.RemoveClusterPeer(endpoint.NodeID, material.CredentialID)
		return errors.Join(
			fmt.Errorf("remote node installed but authenticated NP/2 attestation failed: %w", err),
			wrapClusterPeerRollbackError(rollbackErr),
		)
	}
	reportClusterEnrollment(report, 93, "Committing the node to the cluster catalog")
	now := time.Now().UTC()
	node := cluster.Node{
		ID: draft.nodeID, Name: draft.name, Region: draft.region,
		Roles:          []cluster.NodeRole{cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: draft.domain, PublicAddresses: append([]string(nil), draft.addresses...), NP2Endpoint: draft.domain + ":443",
		HTTPSPath: paths[0], WebRTCPath: paths[1], HTTP3Path: paths[2], Enabled: true, ClientVisible: false,
		CredentialID: material.CredentialID, HostKeySHA256: draft.fingerprint, ProvisionedAt: now, UpdatedAt: now,
	}
	if _, err := manager.UpsertClusterNode(node); err != nil {
		rollbackErr := manager.RemoveClusterPeer(endpoint.NodeID, material.CredentialID)
		return errors.Join(
			fmt.Errorf("peer installed but cluster state commit failed: %w", err),
			wrapClusterPeerRollbackError(rollbackErr),
		)
	}
	reportClusterEnrollment(report, 97, "Restarting the master with the new peer map")
	if err := controller.Action("restart", output, failures); err != nil {
		return fmt.Errorf("node enrolled but master restart failed: %w", err)
	}
	fmt.Fprintf(output, "Node %s enrolled, authenticated and connected to cluster %s.\n", node.ID, state.ClusterID)
	fmt.Fprintln(output, "Client visibility is OFF until explicitly published in CLUSTER.")
	return nil
}

func reportClusterEnrollment(report func(int, string), progress int, stage string) {
	if report != nil {
		report(progress, stage)
	}
}

func zeroClusterEnrollmentDraft(draft *clusterEnrollmentDraft) {
	if draft == nil {
		return
	}
	for index := range draft.password {
		draft.password[index] = 0
	}
	*draft = clusterEnrollmentDraft{}
}

func wrapClusterPeerRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("master peer rollback failed: %w", err)
}

func mutateSelectedClusterNode(manager *admin.Manager, operation tuiOperation, nodeID string) error {
	state, err := manager.ClusterState()
	if err != nil {
		return err
	}
	for _, node := range state.Nodes {
		if node.ID != nodeID {
			continue
		}
		if containsClusterRole(node.Roles, cluster.RoleMaster) {
			return errors.New("master node is protected")
		}
		switch operation {
		case tuiOperationClusterToggleEnabled:
			node.Enabled = !node.Enabled
		case tuiOperationClusterTogglePublished:
			if !node.Enabled && !node.ClientVisible {
				return errors.New("enable the node before publishing it")
			}
			node.ClientVisible = !node.ClientVisible
		case tuiOperationClusterRemove:
			for _, route := range state.Routes {
				if containsString(route.Action.NodeIDs, nodeID) {
					return admin.ErrClusterNodeInUse
				}
			}
			if err := manager.RemoveClusterPeer(node.ID, node.CredentialID); err != nil {
				return err
			}
			_, err := manager.RemoveClusterNode(nodeID)
			return err
		default:
			return errors.New("unsupported cluster node operation")
		}
		_, err = manager.UpsertClusterNode(node)
		return err
	}
	return admin.ErrClusterNodeNotFound
}

func createClusterRoute(manager *admin.Manager, draft clusterRouteDraft) error {
	state, err := manager.ClusterState()
	if err != nil {
		return err
	}
	for _, existing := range state.Routes {
		if existing.ID == draft.id {
			return errors.New("route ID already exists")
		}
	}
	route := cluster.Route{
		ID: draft.id, Name: draft.name, Priority: draft.priority, Enabled: true,
		Source: cluster.RouteSourceAdmin,
		Match: cluster.RouteMatch{Protocols: []cluster.NetworkProtocol{
			cluster.ProtocolTCP, cluster.ProtocolUDP,
		}},
	}
	matchKind, matchValue, found := strings.Cut(strings.TrimSpace(draft.match), ":")
	if !found || matchValue == "" {
		return errors.New("route match must use domain: or cidr:")
	}
	switch strings.ToLower(matchKind) {
	case "domain":
		route.Match.DomainSuffixes = []string{strings.ToLower(strings.TrimSuffix(strings.TrimSpace(matchValue), "."))}
	case "ip", "cidr":
		raw := strings.TrimSpace(matchValue)
		prefix, err := netip.ParsePrefix(raw)
		if address, addressErr := netip.ParseAddr(raw); addressErr == nil {
			prefix = netip.PrefixFrom(address.Unmap(), address.BitLen())
			err = nil
		}
		if err != nil || prefix.String() != prefix.Masked().String() {
			return errors.New("IP/CIDR must be valid and canonical")
		}
		route.Match.CIDRs = []string{prefix.String()}
	case "geoip":
		country := strings.ToLower(strings.TrimSpace(matchValue))
		if len(country) != 2 && country != "private" {
			return errors.New("GeoIP must be a two-letter country code or private")
		}
		route.Match.GeoIPCountries = []string{country}
	case "geosite":
		category := strings.ToLower(strings.TrimSpace(matchValue))
		route.Match.GeoSiteCategories = []string{category}
	default:
		return errors.New("route match must use domain, IP/CIDR, GeoIP or GeoSite")
	}
	actionKind, actionValue, hasValue := strings.Cut(strings.TrimSpace(draft.action), ":")
	switch strings.ToLower(actionKind) {
	case "current", "direct", "block", "auto":
		if hasValue {
			return errors.New("selected route action does not accept node IDs")
		}
		route.Action.Kind = cluster.RouteActionKind(strings.ToLower(actionKind))
	case "node":
		if !hasValue || strings.TrimSpace(actionValue) == "" {
			return errors.New("node action requires one node ID")
		}
		route.Action = cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{strings.TrimSpace(actionValue)}}
	case "chain":
		if !hasValue {
			return errors.New("chain action requires node IDs")
		}
		for _, value := range strings.Split(actionValue, ",") {
			route.Action.NodeIDs = append(route.Action.NodeIDs, strings.TrimSpace(value))
		}
		route.Action.Kind = cluster.RouteActionChain
	default:
		return errors.New("unknown route action")
	}
	if err := cluster.ValidateRoute(route); err != nil {
		return err
	}
	if len(route.Match.GeoIPCountries) > 0 || len(route.Match.GeoSiteCategories) > 0 {
		engine, err := geodata.Load(manager.GeodataDirectory())
		if err != nil {
			return fmt.Errorf("load GeoIP/GeoSite database: %w", err)
		}
		for _, country := range route.Match.GeoIPCountries {
			if !engine.HasGeoIP(country) {
				return fmt.Errorf("GeoIP category %q is not installed", country)
			}
		}
		for _, category := range route.Match.GeoSiteCategories {
			if !engine.HasGeoSite(category) {
				return fmt.Errorf("GeoSite category %q is not installed", category)
			}
		}
	}
	_, err = manager.UpsertClusterRoute(route)
	return err
}

func createClusterRouteForUsersSynced(
	manager *admin.Manager,
	draft clusterRouteDraft,
	synchronize clusterCredentialSynchronizer,
) error {
	if len(draft.userIDs) == 0 {
		return errors.New("select at least one active user")
	}
	before, err := manager.ClusterState()
	if err != nil {
		return err
	}
	previous := make(map[string]*cluster.UserAccess, len(draft.userIDs))
	for _, userID := range draft.userIDs {
		for _, access := range before.Access {
			if access.UserID == userID {
				copyOfAccess := access
				copyOfAccess.AllowedNodeIDs = append([]string(nil), access.AllowedNodeIDs...)
				copyOfAccess.AllowedRouteIDs = append([]string(nil), access.AllowedRouteIDs...)
				previous[userID] = &copyOfAccess
				break
			}
		}
	}
	if err := createClusterRoute(manager, draft); err != nil {
		return err
	}
	assigned := make([]string, 0, len(draft.userIDs))
	for _, userID := range draft.userIDs {
		if err := assignRouteToUserSynced(manager, draft.id, userID, synchronize); err != nil {
			rollbackErrors := []error{fmt.Errorf("assign route to user %s: %w", userID, err)}
			if _, removeErr := manager.RemoveClusterRoute(draft.id); removeErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove failed route: %w", removeErr))
			}
			for index := len(assigned) - 1; index >= 0; index-- {
				assignedUser := assigned[index]
				old := previous[assignedUser]
				if old == nil {
					if _, restoreErr := manager.RemoveClusterUserAccess(assignedUser); restoreErr != nil && !errors.Is(restoreErr, admin.ErrUserNotFound) {
						rollbackErrors = append(rollbackErrors, restoreErr)
					}
					if restoreErr := synchronize(manager, assignedUser, nil); restoreErr != nil {
						rollbackErrors = append(rollbackErrors, restoreErr)
					}
					continue
				}
				if _, restoreErr := manager.SetClusterUserAccess(*old); restoreErr != nil {
					rollbackErrors = append(rollbackErrors, restoreErr)
				}
				if restoreErr := synchronize(manager, assignedUser, old.AllowedNodeIDs); restoreErr != nil {
					rollbackErrors = append(rollbackErrors, restoreErr)
				}
			}
			return errors.Join(rollbackErrors...)
		}
		assigned = append(assigned, userID)
	}
	return nil
}

func mutateClusterRoute(manager *admin.Manager, operation tuiOperation, routeID string) error {
	state, err := manager.ClusterState()
	if err != nil {
		return err
	}
	for _, route := range state.Routes {
		if route.ID != routeID {
			continue
		}
		switch operation {
		case tuiOperationRouteToggleEnabled:
			route.Enabled = !route.Enabled
			_, err = manager.UpsertClusterRoute(route)
			return err
		case tuiOperationRouteRemove:
			_, err = manager.RemoveClusterRoute(routeID)
			return err
		}
	}
	return admin.ErrClusterRouteNotFound
}

func toggleUserClusterAccess(manager *admin.Manager, userID string) (bool, error) {
	state, err := manager.ClusterState()
	if err != nil {
		return false, err
	}
	for _, access := range state.Access {
		if access.UserID == userID {
			_, err := manager.RemoveClusterUserAccess(userID)
			return false, err
		}
	}
	access := cluster.UserAccess{UserID: userID, AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1}
	for _, node := range state.Nodes {
		if node.Enabled {
			access.AllowedNodeIDs = append(access.AllowedNodeIDs, node.ID)
		}
	}
	for _, route := range state.Routes {
		if route.Enabled {
			access.AllowedRouteIDs = append(access.AllowedRouteIDs, route.ID)
		}
	}
	_, err = manager.SetClusterUserAccess(access)
	return true, err
}

type clusterCredentialSynchronizer func(*admin.Manager, string, []string) error

func toggleClusterNodeForUserSynced(
	manager *admin.Manager,
	nodeID, userID string,
	synchronize clusterCredentialSynchronizer,
) (bool, error) {
	state, err := manager.ClusterState()
	if err != nil {
		return false, err
	}
	selectedExists := false
	masterID := ""
	for _, node := range state.Nodes {
		if containsClusterRole(node.Roles, cluster.RoleMaster) {
			masterID = node.ID
		}
		if node.ID == nodeID {
			if containsClusterRole(node.Roles, cluster.RoleMaster) {
				return false, errors.New("master access is required and cannot be toggled")
			}
			if !node.Enabled {
				return false, errors.New("enable the node before assigning it")
			}
			selectedExists = true
		}
	}
	if !selectedExists || masterID == "" {
		return false, admin.ErrClusterNodeNotFound
	}
	proposed := cluster.UserAccess{
		UserID: userID, AllowedNodeIDs: []string{masterID},
		AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1,
	}
	var previous *cluster.UserAccess
	for _, current := range state.Access {
		if current.UserID != userID {
			continue
		}
		copyOfCurrent := current
		previous = &copyOfCurrent
		proposed = current
		if !containsString(proposed.AllowedNodeIDs, masterID) {
			proposed.AllowedNodeIDs = append([]string{masterID}, proposed.AllowedNodeIDs...)
		}
		break
	}
	enabled := !containsString(proposed.AllowedNodeIDs, nodeID)
	if enabled {
		proposed.AllowedNodeIDs = append(proposed.AllowedNodeIDs, nodeID)
	} else {
		filtered := proposed.AllowedNodeIDs[:0]
		for _, allowedNodeID := range proposed.AllowedNodeIDs {
			if allowedNodeID != nodeID {
				filtered = append(filtered, allowedNodeID)
			}
		}
		proposed.AllowedNodeIDs = filtered
	}
	previousNodes := []string(nil)
	if previous != nil {
		previousNodes = append(previousNodes, previous.AllowedNodeIDs...)
	}
	if err := synchronize(manager, userID, proposed.AllowedNodeIDs); err != nil {
		rollbackErr := synchronize(manager, userID, previousNodes)
		return false, errors.Join(
			fmt.Errorf("edge credential pre-sync failed: %w", err),
			wrapRollbackError(rollbackErr),
		)
	}
	if _, err := manager.SetClusterUserAccess(proposed); err != nil {
		rollbackErr := synchronize(manager, userID, previousNodes)
		return false, errors.Join(err, wrapRollbackError(rollbackErr))
	}
	return enabled, nil
}

func toggleUserClusterAccessSynced(
	manager *admin.Manager,
	userID string,
	synchronize clusterCredentialSynchronizer,
) (bool, error) {
	state, err := manager.ClusterState()
	if err != nil {
		return false, err
	}
	for _, access := range state.Access {
		if access.UserID != userID {
			continue
		}
		if _, err := manager.RemoveClusterUserAccess(userID); err != nil {
			return false, err
		}
		if err := synchronize(manager, userID, nil); err != nil {
			return false, fmt.Errorf("access revoked locally; edge credential revocation failed: %w", err)
		}
		return false, nil
	}
	access := newDefaultClusterUserAccess(state, userID)
	if err := synchronize(manager, userID, access.AllowedNodeIDs); err != nil {
		rollbackErr := synchronize(manager, userID, nil)
		return false, errors.Join(
			fmt.Errorf("edge credential pre-sync failed: %w", err),
			wrapRollbackError(rollbackErr),
		)
	}
	if _, err := manager.SetClusterUserAccess(access); err != nil {
		rollbackErr := synchronize(manager, userID, nil)
		return false, errors.Join(err, wrapRollbackError(rollbackErr))
	}
	return true, nil
}

func assignRouteToUser(manager *admin.Manager, routeID, userID string) error {
	state, access, _, err := proposedRouteAssignment(manager, routeID, userID)
	if err != nil {
		return err
	}
	_ = state
	_, err = manager.SetClusterUserAccess(access)
	return err
}

func assignRouteToUserSynced(
	manager *admin.Manager,
	routeID, userID string,
	synchronize clusterCredentialSynchronizer,
) error {
	_, proposed, previous, err := proposedRouteAssignment(manager, routeID, userID)
	if err != nil {
		return err
	}
	previousNodes := []string(nil)
	if previous != nil {
		previousNodes = append(previousNodes, previous.AllowedNodeIDs...)
	}
	if err := synchronize(manager, userID, proposed.AllowedNodeIDs); err != nil {
		rollbackErr := synchronize(manager, userID, previousNodes)
		return errors.Join(
			fmt.Errorf("edge credential pre-sync failed: %w", err),
			wrapRollbackError(rollbackErr),
		)
	}
	if _, err := manager.SetClusterUserAccess(proposed); err != nil {
		rollbackErr := synchronize(manager, userID, previousNodes)
		return errors.Join(err, wrapRollbackError(rollbackErr))
	}
	return nil
}

func proposedRouteAssignment(
	manager *admin.Manager,
	routeID, userID string,
) (cluster.State, cluster.UserAccess, *cluster.UserAccess, error) {
	state, err := manager.ClusterState()
	if err != nil {
		return cluster.State{}, cluster.UserAccess{}, nil, err
	}
	var selectedRoute *cluster.Route
	for _, route := range state.Routes {
		if route.ID == routeID {
			copyOfRoute := route
			selectedRoute = &copyOfRoute
			break
		}
	}
	if selectedRoute == nil {
		return cluster.State{}, cluster.UserAccess{}, nil, admin.ErrClusterRouteNotFound
	}
	access := cluster.UserAccess{UserID: userID, AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1}
	var previous *cluster.UserAccess
	for _, current := range state.Access {
		if current.UserID == userID {
			access = current
			copyOfCurrent := current
			previous = &copyOfCurrent
			break
		}
	}
	requiredNodes := make(map[string]struct{}, len(selectedRoute.Action.NodeIDs)+1)
	for _, node := range state.Nodes {
		if containsClusterRole(node.Roles, cluster.RoleMaster) {
			requiredNodes[node.ID] = struct{}{}
		}
		if selectedRoute.Action.Kind == cluster.RouteActionAuto && node.Enabled {
			requiredNodes[node.ID] = struct{}{}
		}
	}
	for _, nodeID := range selectedRoute.Action.NodeIDs {
		requiredNodes[nodeID] = struct{}{}
	}
	for _, node := range state.Nodes {
		if _, required := requiredNodes[node.ID]; required && !containsString(access.AllowedNodeIDs, node.ID) {
			access.AllowedNodeIDs = append(access.AllowedNodeIDs, node.ID)
		}
	}
	if !containsString(access.AllowedRouteIDs, routeID) {
		access.AllowedRouteIDs = append(access.AllowedRouteIDs, routeID)
	}
	return state, access, previous, nil
}

func newDefaultClusterUserAccess(state cluster.State, userID string) cluster.UserAccess {
	access := cluster.UserAccess{UserID: userID, AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1}
	for _, node := range state.Nodes {
		if node.Enabled {
			access.AllowedNodeIDs = append(access.AllowedNodeIDs, node.ID)
		}
	}
	for _, route := range state.Routes {
		if route.Enabled {
			access.AllowedRouteIDs = append(access.AllowedRouteIDs, route.ID)
		}
	}
	return access
}

func wrapRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("credential rollback failed: %w", err)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func newClusterTransportPaths() ([3]string, error) {
	var result [3]string
	for index := range result {
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			return [3]string{}, err
		}
		result[index] = "/" + hex.EncodeToString(raw)
		for position := range raw {
			raw[position] = 0
		}
	}
	if result[0] == result[1] || result[0] == result[2] || result[1] == result[2] {
		return [3]string{}, errors.New("generated duplicate transport paths")
	}
	return result, nil
}

func clusterNodeActionLabel(operation tuiOperation) string {
	switch operation {
	case tuiOperationClusterToggleEnabled:
		return "node traffic state updated"
	case tuiOperationClusterTogglePublished:
		return "client visibility updated"
	case tuiOperationClusterRemove:
		return "node removed from cluster state"
	default:
		return strings.ToLower(operation.String())
	}
}

func (operation tuiOperation) String() string { return fmt.Sprintf("operation-%d", operation) }
