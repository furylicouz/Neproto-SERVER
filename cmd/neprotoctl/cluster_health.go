package main

import (
	"context"
	"net"
	"sync"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

type clusterNodeHealth struct {
	status  string
	latency time.Duration
	checked time.Time
}

func probeClusterNodes(nodes []cluster.Node, timeout time.Duration) map[string]clusterNodeHealth {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	result := make(map[string]clusterNodeHealth, len(nodes))
	var mutex sync.Mutex
	var workers sync.WaitGroup
	for _, node := range nodes {
		node := node
		if !node.Enabled {
			mutex.Lock()
			result[node.ID] = clusterNodeHealth{status: "DRAIN", checked: time.Now()}
			mutex.Unlock()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", node.NP2Endpoint)
			if connection != nil {
				_ = connection.Close()
			}
			health := clusterNodeHealth{status: "DOWN", checked: time.Now()}
			if err == nil {
				health.status = "UP"
				health.latency = time.Since(started)
			}
			mutex.Lock()
			result[node.ID] = health
			mutex.Unlock()
		}()
	}
	workers.Wait()
	return result
}
