package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxClusterNodeStateBytes = 64 << 10

type clusterNodeState struct {
	Version      int      `json:"version"`
	ClusterID    string   `json:"cluster_id"`
	NodeID       string   `json:"node_id"`
	Name         string   `json:"name"`
	Region       string   `json:"region"`
	Roles        []string `json:"roles"`
	MasterNodeID string   `json:"master_node_id"`
	InstalledAt  string   `json:"installed_at"`
}

func loadClusterNodeState(path string) (clusterNodeState, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxClusterNodeStateBytes {
		return clusterNodeState{}, errors.New("invalid cluster node state file")
	}
	file, err := os.Open(path)
	if err != nil {
		return clusterNodeState{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxClusterNodeStateBytes+1))
	if err != nil || len(raw) > maxClusterNodeStateBytes {
		return clusterNodeState{}, errors.New("invalid cluster node state file")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state clusterNodeState
	if err := decoder.Decode(&state); err != nil {
		return clusterNodeState{}, errors.New("invalid cluster node state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return clusterNodeState{}, errors.New("invalid cluster node state")
	}
	if !validClusterIdentifier(state.ClusterID) || !validClusterIdentifier(state.NodeID) ||
		!validClusterIdentifier(state.MasterNodeID) || !validClusterLabel(state.Name) ||
		!validClusterLabel(state.Region) || len(state.Roles) == 0 || len(state.Roles) > 4 {
		return clusterNodeState{}, errors.New("invalid cluster node state")
	}
	seen := make(map[string]struct{}, len(state.Roles))
	for _, role := range state.Roles {
		if role != "ingress" && role != "relay" && role != "egress" {
			return clusterNodeState{}, errors.New("invalid cluster node role")
		}
		if _, duplicate := seen[role]; duplicate {
			return clusterNodeState{}, errors.New("duplicate cluster node role")
		}
		seen[role] = struct{}{}
	}
	installedAt, err := time.Parse(time.RFC3339, state.InstalledAt)
	if err != nil || installedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return clusterNodeState{}, errors.New("invalid cluster installation time")
	}
	return state, nil
}

func clusterAttestation(arguments []string, stdout, stderr io.Writer) int {
	statePath := "/etc/neproto/cluster/node.json"
	format := "token"
	for len(arguments) > 0 {
		switch arguments[0] {
		case "--state":
			if len(arguments) < 2 {
				fmt.Fprintln(stderr, "cluster attestation requires --state PATH")
				return 2
			}
			statePath, arguments = arguments[1], arguments[2:]
		case "--format":
			if len(arguments) < 2 {
				fmt.Fprintln(stderr, "cluster attestation requires --format token|json")
				return 2
			}
			format, arguments = arguments[1], arguments[2:]
		default:
			fmt.Fprintln(stderr, "invalid cluster attestation arguments")
			return 2
		}
	}
	state, err := loadClusterNodeState(filepath.Clean(statePath))
	if err != nil {
		fmt.Fprintf(stderr, "cluster node attestation failed: %v\n", err)
		return 1
	}
	switch format {
	case "token":
		fmt.Fprintln(stdout, "NP2_CLUSTER_NODE_READY")
	case "json":
		encoded, _ := json.Marshal(map[string]any{"ready": true, "cluster_id": state.ClusterID, "node_id": state.NodeID})
		fmt.Fprintln(stdout, string(encoded))
	default:
		fmt.Fprintln(stderr, "cluster attestation format must be token or json")
		return 2
	}
	return 0
}

func validClusterIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validClusterLabel(value string) bool {
	return value != "" && len(value) <= 96 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
}
