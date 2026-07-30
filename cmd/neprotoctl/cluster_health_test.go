package main

import (
	"net"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

func TestProbeClusterNodesReportsReachableDownAndDrained(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	nodes := []cluster.Node{
		{ID: "up", Enabled: true, NP2Endpoint: listener.Addr().String()},
		{ID: "down", Enabled: true, NP2Endpoint: "127.0.0.1:1"},
		{ID: "drain", Enabled: false, NP2Endpoint: "127.0.0.1:2"},
	}
	health := probeClusterNodes(nodes, 300*time.Millisecond)
	if health["up"].status != "UP" || health["down"].status != "DOWN" || health["drain"].status != "DRAIN" {
		t.Fatalf("unexpected cluster health: %+v", health)
	}
}
