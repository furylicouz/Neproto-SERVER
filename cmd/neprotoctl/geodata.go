package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/geodata"
)

type geoDataProgress func(int, string)

func geodataCommand(manager *admin.Manager, controller serviceController, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		return usage(stderr)
	}
	if arguments[0] == "schedule" {
		flags := flag.NewFlagSet("geodata schedule", flag.ContinueOnError)
		flags.SetOutput(stderr)
		preset := flags.String("preset", "", "daily, weekly, monthly, or off")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *preset == "" {
			return usage(stderr)
		}
		if err := setGeoDataSchedule(manager.RootDirectory(), *preset); err != nil {
			fmt.Fprintf(stderr, "geodata schedule failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "GeoData automatic update schedule: %s\n", *preset)
		return 0
	}
	if arguments[0] != "status" && arguments[0] != "update" {
		return usage(stderr)
	}
	flags := flag.NewFlagSet("geodata "+arguments[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	clusterWide := flags.Bool("cluster", true, "apply to every configured cluster node")
	if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 {
		return usage(stderr)
	}
	operation := cluster.GeoDataStatus
	if arguments[0] == "update" {
		operation = cluster.GeoDataUpdate
	}
	statuses, err := runGeoDataOperation(manager, controller, operation, *clusterWide, nil, stdout, stderr)
	for _, status := range statuses {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", status.NodeID, status.State, shortHash(status.GeoIPSHA256), shortHash(status.GeoSiteSHA256))
	}
	if err != nil {
		fmt.Fprintf(stderr, "geodata %s failed: %v\n", arguments[0], err)
		return 1
	}
	return 0
}

func runGeoDataOperation(
	manager *admin.Manager,
	controller serviceController,
	operation cluster.GeoDataOperation,
	clusterWide bool,
	progress geoDataProgress,
	stdout, stderr io.Writer,
) ([]cluster.GeoDataNodeStatus, error) {
	if manager == nil || controller == nil || cluster.ValidateGeoDataRequest(cluster.GeoDataRequest{Version: 1, Operation: operation}) != nil {
		return nil, errors.New("invalid geodata operation")
	}
	if operation == cluster.GeoDataUpdate {
		release, err := acquireGeoDataOrchestrationLock(manager.GeodataDirectory())
		if err != nil {
			return nil, err
		}
		defer release()
	}
	report := func(value int, stage string) {
		if progress != nil {
			progress(value, stage)
		}
	}
	server, err := config.LoadServer(manager.ServerConfigPath())
	if err != nil {
		return nil, err
	}
	localNodeID := server.ClusterNodeID
	if localNodeID == "" {
		localNodeID = "local"
	}
	report(5, "Inspecting local GeoIP and GeoSite databases")
	local, localErr := localGeoDataOperation(operation, manager.GeodataDirectory())
	if operation == cluster.GeoDataUpdate && localErr == nil {
		localErr = secureGeoDataForService(manager)
	}
	localStatus := geoDataNodeStatus(localNodeID, local, localErr)
	statuses := []cluster.GeoDataNodeStatus{localStatus}
	var failures []error
	if localErr != nil {
		failures = append(failures, fmt.Errorf("%s: %w", localNodeID, localErr))
	}

	if clusterWide && server.ClusterNodeID != "" && server.ClusterNodeID == server.ClusterMasterNodeID {
		peerConfigs, peerErr := clusterrelay.LoadPeerConfigs(server.ClusterPeerDirectory)
		if peerErr != nil {
			failures = append(failures, fmt.Errorf("load cluster peers: %w", peerErr))
		} else if len(peerConfigs) > 0 {
			pool, poolErr := clusterrelay.NewPeerPool(peerConfigs, app.ConnectClientHTTPSFirst)
			if poolErr != nil {
				failures = append(failures, poolErr)
			} else {
				defer pool.Close()
				nodeIDs := make([]string, 0, len(peerConfigs))
				for nodeID := range peerConfigs {
					nodeIDs = append(nodeIDs, nodeID)
				}
				sort.Strings(nodeIDs)
				for index, nodeID := range nodeIDs {
					report(15+(index*65/maxInt(1, len(nodeIDs))), "Updating geodata on cluster node "+nodeID)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					payload, remoteErr := pool.GeoData(ctx, nodeID, cluster.GeoDataRequest{Version: 1, Operation: operation})
					cancel()
					if remoteErr != nil {
						failures = append(failures, fmt.Errorf("%s: %w", nodeID, remoteErr))
						statuses = append(statuses, cluster.GeoDataNodeStatus{Version: 1, NodeID: nodeID, State: geodata.UpdateStateError, Error: remoteErr.Error()})
						continue
					}
					remote, decodeErr := decodeGeoDataNodeStatus(payload, nodeID)
					if decodeErr != nil {
						failures = append(failures, fmt.Errorf("%s: %w", nodeID, decodeErr))
						continue
					}
					statuses = append(statuses, remote)
					if remote.State != geodata.UpdateStateReady {
						failures = append(failures, fmt.Errorf("%s: %s", nodeID, remote.Error))
					} else if operation == cluster.GeoDataUpdate && localErr == nil &&
						(remote.GeoIPSHA256 != localStatus.GeoIPSHA256 || remote.GeoSiteSHA256 != localStatus.GeoSiteSHA256) {
						failures = append(failures, fmt.Errorf("%s: geodata snapshot differs from master", nodeID))
					}
				}
			}
		}
	}
	if operation == cluster.GeoDataUpdate && localErr == nil {
		report(90, "Reloading the master NP/2 routing engine")
		if err := controller.Action("restart-np2", stdout, stderr); err != nil {
			failures = append(failures, fmt.Errorf("restart master routing engine: %w", err))
		}
	}
	report(99, "GeoData operation completed on all reachable nodes")
	return statuses, errors.Join(failures...)
}

func acquireGeoDataOrchestrationLock(directory string) (func(), error) {
	path := filepath.Join(directory, ".cluster-update.lock")
	open := func() (*os.File, error) {
		return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	file, err := open()
	if err != nil && errors.Is(err, os.ErrExist) {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > 30*time.Minute {
			if removeErr := os.Remove(path); removeErr == nil {
				file, err = open()
			}
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, geodata.ErrUpdateInProgress
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Close()
	return func() { _ = os.Remove(path) }, nil
}

func secureGeoDataForService(manager *admin.Manager) error {
	installation := manager.Installation()
	if installation.ServiceGID == nil {
		return errors.New("service group is unavailable")
	}
	directory := manager.GeodataDirectory()
	if err := os.Chown(directory, -1, *installation.ServiceGID); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		path := filepath.Join(directory, name)
		if err := os.Chown(path, -1, *installation.ServiceGID); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func localGeoDataOperation(operation cluster.GeoDataOperation, directory string) (geodata.UpdateStatus, error) {
	if operation == cluster.GeoDataUpdate {
		return geodata.DefaultUpdater().Update(context.Background(), directory)
	}
	return geodata.Status(directory)
}

func geoDataNodeStatus(nodeID string, status geodata.UpdateStatus, err error) cluster.GeoDataNodeStatus {
	result := cluster.GeoDataNodeStatus{
		Version: 1, NodeID: nodeID, State: status.State, UpdatedAt: status.UpdatedAt,
		GeoIPSHA256: status.GeoIPSHA256, GeoSiteSHA256: status.GeoSiteSHA256,
		GeoIPBytes: status.GeoIPBytes, GeoSiteBytes: status.GeoSiteBytes,
	}
	if err != nil {
		result.State = geodata.UpdateStateError
		result.Error = boundedGeoDataError(err)
	}
	return result
}

func decodeGeoDataNodeStatus(payload []byte, expectedNodeID string) (cluster.GeoDataNodeStatus, error) {
	if len(payload) == 0 || len(payload) > 16<<10 {
		return cluster.GeoDataNodeStatus{}, errors.New("invalid geodata status payload")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var status cluster.GeoDataNodeStatus
	if err := decoder.Decode(&status); err != nil {
		return cluster.GeoDataNodeStatus{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || status.Version != 1 || status.NodeID != expectedNodeID ||
		(status.State != geodata.UpdateStateReady && status.State != geodata.UpdateStateError) {
		return cluster.GeoDataNodeStatus{}, errors.New("invalid geodata node status")
	}
	return status, nil
}

func shortHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func boundedGeoDataError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func geoDataSchedule(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "etc", "neproto", "geodata-schedule"))
	if err != nil {
		return "unknown"
	}
	preset := strings.TrimSpace(string(raw))
	if validGeoDataSchedule(preset) {
		return preset
	}
	return "unknown"
}

func setGeoDataSchedule(root, preset string) error {
	if root == "" || !filepath.IsAbs(root) || !validGeoDataSchedule(preset) {
		return errors.New("schedule must be daily, weekly, monthly, or off")
	}
	stateDirectory := filepath.Join(root, "etc", "neproto")
	dropInDirectory := filepath.Join(root, "etc", "systemd", "system", "neproto-geodata-update.timer.d")
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(dropInDirectory, 0o755); err != nil {
		return err
	}
	calendar, jitter := geoDataCalendar(preset)
	configuration := "[Timer]\nOnCalendar=\n"
	if preset != "off" {
		configuration += "OnCalendar=" + calendar + "\nRandomizedDelaySec=" + jitter + "\nPersistent=true\n"
	}
	if err := writeAtomicText(filepath.Join(dropInDirectory, "schedule.conf"), configuration, 0o644); err != nil {
		return err
	}
	if err := writeAtomicText(filepath.Join(stateDirectory, "geodata-schedule"), preset+"\n", 0o644); err != nil {
		return err
	}
	if filepath.Clean(root) != string(os.PathSeparator) {
		return nil
	}
	commands := [][]string{{"daemon-reload"}}
	if preset == "off" {
		commands = append(commands, []string{"disable", "--now", "neproto-geodata-update.timer"})
	} else {
		commands = append(commands, []string{"enable", "--now", "neproto-geodata-update.timer"})
	}
	for _, arguments := range commands {
		if output, err := exec.Command("systemctl", arguments...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func validGeoDataSchedule(value string) bool {
	return value == "daily" || value == "weekly" || value == "monthly" || value == "off"
}

func geoDataCalendar(preset string) (string, string) {
	switch preset {
	case "daily":
		return "*-*-* 04:15:00", "2h"
	case "monthly":
		return "*-*-01 04:15:00", "12h"
	default:
		return "Mon *-*-* 04:15:00", "6h"
	}
}

func writeAtomicText(path, value string, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".geodata-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(value); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
