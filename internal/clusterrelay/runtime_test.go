package clusterrelay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

func TestMasterRoutesAssignedUserFlowToNP2Peer(t *testing.T) {
	state := relayState()
	var openedNode string
	var openedRequest cluster.RelayRequest
	runtime := &Runtime{
		NodeID: "master", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge": "edge-01"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(_ context.Context, nodeID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
			openedNode, openedRequest = nodeID, request
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) {
			t.Fatal("target was dialed on ingress")
			return nil, nil
		},
		DialUDP: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		Random:  bytes.NewReader([]byte("12345678")),
	}
	stream, handled, err := runtime.RouteTCP(context.Background(), "alice", proxy.Target{Host: "www.youtube.com", Port: 443})
	if err != nil || !handled || stream == nil || openedNode != "edge-01" {
		t.Fatalf("stream=%v handled=%v node=%q err=%v", stream, handled, openedNode, err)
	}
	if openedRequest.RouteID != "media" || openedRequest.UserID != "alice" || openedRequest.TraceID != "3132333435363738" ||
		len(openedRequest.VisitedNodeIDs) != 1 || openedRequest.VisitedNodeIDs[0] != "master" {
		t.Fatalf("relay request=%+v", openedRequest)
	}
}

func TestEdgeForwardsUnresolvedUserFlowToMaster(t *testing.T) {
	var request cluster.RelayRequest
	runtime := &Runtime{
		NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
		OpenPeer: func(_ context.Context, nodeID string, candidate cluster.RelayRequest) (proxy.DuplexStream, error) {
			if nodeID != "master" {
				t.Fatalf("nodeID=%q", nodeID)
			}
			request = candidate
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		Random:     bytes.NewReader([]byte("abcdefgh")),
	}
	_, handled, err := runtime.RouteTCP(context.Background(), "alice", proxy.Target{Host: "example.org", Port: 443})
	if err != nil || !handled || request.RouteID != "resolve" || request.RemainingHops != 1 || request.VisitedNodeIDs[0] != "edge-01" {
		t.Fatalf("request=%+v handled=%v err=%v", request, handled, err)
	}
}

func TestEdgeUsesSelectedNodeAsDefaultEgressWithReplicatedState(t *testing.T) {
	state := relayState()
	state.Routes = nil
	state.Access[0].AllowedRouteIDs = nil
	peerOpens := 0
	runtime := &Runtime{
		NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error) {
			peerOpens++
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
	}

	stream, handled, err := runtime.RouteTCP(
		context.Background(), "alice", proxy.Target{Host: "example.org", Port: 443},
	)
	if err != nil || handled || stream != nil || peerOpens != 0 {
		t.Fatalf("stream=%v handled=%v peer_opens=%d err=%v", stream, handled, peerOpens, err)
	}
}

func TestEdgeRoutesExplicitOtherNodeThroughMasterStarTopology(t *testing.T) {
	state := relayState()
	now := time.Now().UTC()
	state.Nodes = append(state.Nodes, cluster.Node{
		ID: "edge-02", Name: "Edge 02", Region: "Netherlands", Roles: []cluster.NodeRole{cluster.RoleEgress},
		PublicIdentity: "edge-02.example.com", PublicAddresses: []string{"9.9.9.9"}, NP2Endpoint: "edge-02.example.com:443",
		Enabled: true, ClientVisible: true, CredentialID: "peer-edge-02", HostKeySHA256: "SHA256:edge-02",
		ProvisionedAt: now, UpdatedAt: now,
	})
	state.Routes[0].Action.NodeIDs = []string{"edge-02"}
	state.Access[0].AllowedNodeIDs = append(state.Access[0].AllowedNodeIDs, "edge-02")
	var openedNode string
	var openedRequest cluster.RelayRequest
	runtime := &Runtime{
		NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(_ context.Context, nodeID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
			openedNode, openedRequest = nodeID, request
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		Random:     bytes.NewReader([]byte("starpath")),
	}
	stream, handled, err := runtime.RouteTCP(
		context.Background(), "alice", proxy.Target{Host: "www.youtube.com", Port: 443},
	)
	if err != nil || !handled || stream == nil || openedNode != "master" {
		t.Fatalf("stream=%v handled=%v node=%q err=%v", stream, handled, openedNode, err)
	}
	if openedRequest.RemainingHops != 2 || len(openedRequest.RemainingNodeIDs) != 1 ||
		openedRequest.RemainingNodeIDs[0] != "edge-02" || len(openedRequest.VisitedNodeIDs) != 1 ||
		openedRequest.VisitedNodeIDs[0] != "edge-01" {
		t.Fatalf("request=%+v", openedRequest)
	}
}

func TestEdgeReauthorizesClientCurrentNodeRouteLocally(t *testing.T) {
	state := relayState()
	state.Access[0].AllowClientRoutes = true
	peerOpens := 0
	runtime := &Runtime{
		NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error) {
			peerOpens++
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
	}
	request := cluster.ClientRouteRequest{
		Version: cluster.ClientRouteVersion, RouteID: "local-edge",
		Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}},
	}
	stream, handled, err := runtime.RouteClientTCP(
		context.Background(), "alice", proxy.Target{Host: "example.org", Port: 443}, request,
	)
	if err != nil || handled || stream != nil || peerOpens != 0 {
		t.Fatalf("stream=%v handled=%v peer_opens=%d err=%v", stream, handled, peerOpens, err)
	}
}

func TestMasterTerminatesAuthorizedResolveRelayAndRejectsUserCredential(t *testing.T) {
	state := relayState()
	dials := 0
	runtime := &Runtime{
		NodeID: "master", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge": "edge-01"},
		LoadState: func() (cluster.State, error) {
			state.Routes = nil
			state.Access[0].AllowedRouteIDs = nil
			return state, nil
		},
		OpenPeer: func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error) {
			return &nopDuplex{}, nil
		},
		DialTarget: func(_ context.Context, target proxy.Target) (proxy.DuplexStream, error) {
			dials++
			if target.Host != "example.org" {
				t.Fatalf("target=%+v", target)
			}
			return &nopDuplex{}, nil
		},
		DialUDP: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
	}
	request := cluster.RelayRequest{
		Version: cluster.RelayVersion, RouteID: "resolve", UserID: "alice", RemainingHops: 1,
		VisitedNodeIDs: []string{"edge-01"}, TraceID: "0123456789abcdef",
		TargetHost: "example.org", TargetPort: 443, Protocol: cluster.ProtocolTCP,
	}
	if _, err := runtime.HandleRelay(context.Background(), "peer-edge", request); err != nil || dials != 1 {
		t.Fatalf("authorized relay err=%v dials=%d", err, dials)
	}
	if _, err := runtime.HandleRelay(context.Background(), "alice", request); err != ErrRelayUnauthorized {
		t.Fatalf("user credential relay error=%v", err)
	}
}

func TestMasterRoutesUDPToPeerAndTerminatesUDPRelay(t *testing.T) {
	state := relayState()
	state.Routes[0].Match.Protocols = []cluster.NetworkProtocol{cluster.ProtocolUDP}
	var opened cluster.RelayRequest
	udpDials := 0
	runtime := &Runtime{
		NodeID: "master", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge": "edge-01"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(_ context.Context, _ string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
			opened = request
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) {
			t.Fatal("TCP dialer called for UDP")
			return nil, nil
		},
		DialUDP: func(context.Context, proxy.Target) (proxy.DuplexStream, error) {
			udpDials++
			return &nopDuplex{}, nil
		},
		Random: bytes.NewReader([]byte("udp-test")),
	}
	if _, handled, err := runtime.RouteUDP(
		context.Background(), "alice", proxy.Target{Host: "www.youtube.com", Port: 443},
	); err != nil || !handled || opened.Protocol != cluster.ProtocolUDP {
		t.Fatalf("handled=%v request=%+v err=%v", handled, opened, err)
	}
	resolve := cluster.RelayRequest{
		Version: cluster.RelayVersion, RouteID: "resolve", UserID: "alice", RemainingHops: 1,
		VisitedNodeIDs: []string{"edge-01"}, TraceID: "0123456789abcdef",
		TargetHost: "example.org", TargetPort: 443, Protocol: cluster.ProtocolUDP,
	}
	state.Routes = nil
	state.Access[0].AllowedRouteIDs = nil
	if _, err := runtime.HandleRelay(context.Background(), "peer-edge", resolve); err != nil || udpDials != 1 {
		t.Fatalf("UDP terminal err=%v dials=%d", err, udpDials)
	}
}

func TestMasterRoutesBoundedTwoHopChainForTCPAndUDP(t *testing.T) {
	for _, networkProtocol := range []cluster.NetworkProtocol{cluster.ProtocolTCP, cluster.ProtocolUDP} {
		t.Run(string(networkProtocol), func(t *testing.T) {
			state := relayState()
			now := time.Now().UTC()
			state.Nodes = append(state.Nodes, cluster.Node{
				ID: "edge-02", Name: "Edge 02", Region: "Sweden", Roles: []cluster.NodeRole{cluster.RoleRelay, cluster.RoleEgress},
				PublicIdentity: "edge-02.example.com", PublicAddresses: []string{"9.9.9.9"}, NP2Endpoint: "edge-02.example.com:443",
				Enabled: true, ClientVisible: true, CredentialID: "peer-edge-02", HostKeySHA256: "SHA256:edge-02",
				ProvisionedAt: now, UpdatedAt: now,
			})
			state.Routes[0].Match.Protocols = []cluster.NetworkProtocol{networkProtocol}
			state.Routes[0].Action = cluster.RouteAction{
				Kind: cluster.RouteActionChain, NodeIDs: []string{"edge-01", "edge-02"},
			}
			state.Access[0].AllowedNodeIDs = append(state.Access[0].AllowedNodeIDs, "edge-02")
			tcpDials, udpDials := 0, 0
			edge02 := &Runtime{
				NodeID: "edge-02", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge-01": "edge-01"},
				OpenPeer: func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error) {
					t.Fatal("edge-02 attempted another hop")
					return nil, nil
				},
				DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) {
					tcpDials++
					return &nopDuplex{}, nil
				},
				DialUDP: func(context.Context, proxy.Target) (proxy.DuplexStream, error) {
					udpDials++
					return &nopDuplex{}, nil
				},
			}
			edge01 := &Runtime{
				NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
				OpenPeer: func(ctx context.Context, nodeID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
					if nodeID != "edge-02" {
						t.Fatalf("edge-01 next node=%q", nodeID)
					}
					return edge02.HandleRelay(ctx, "peer-edge-01", request)
				},
				DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
				DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
			}
			master := &Runtime{
				NodeID: "master", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge-01": "edge-01"},
				LoadState: func() (cluster.State, error) { return state, nil },
				OpenPeer: func(ctx context.Context, nodeID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
					if nodeID != "edge-01" {
						t.Fatalf("master first node=%q", nodeID)
					}
					return edge01.HandleRelay(ctx, "peer-master", request)
				},
				DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
				DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
				Random:     bytes.NewReader([]byte("two-hops")),
			}
			var stream proxy.DuplexStream
			var handled bool
			var err error
			if networkProtocol == cluster.ProtocolTCP {
				stream, handled, err = master.RouteTCP(context.Background(), "alice", proxy.Target{Host: "www.youtube.com", Port: 443})
			} else {
				stream, handled, err = master.RouteUDP(context.Background(), "alice", proxy.Target{Host: "www.youtube.com", Port: 443})
			}
			if err != nil || !handled || stream == nil {
				t.Fatalf("stream=%v handled=%v err=%v", stream, handled, err)
			}
			if networkProtocol == cluster.ProtocolTCP && (tcpDials != 1 || udpDials != 0) {
				t.Fatalf("TCP dials=%d UDP dials=%d", tcpDials, udpDials)
			}
			if networkProtocol == cluster.ProtocolUDP && (tcpDials != 0 || udpDials != 1) {
				t.Fatalf("TCP dials=%d UDP dials=%d", tcpDials, udpDials)
			}
		})
	}
}

func TestClientRouteHintIsReauthorizedByMasterAndForwardedFromEdge(t *testing.T) {
	state := relayState()
	state.Access[0].AllowClientRoutes = true
	request := cluster.ClientRouteRequest{
		Version: cluster.ClientRouteVersion, RouteID: "local-edge",
		Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}},
	}
	var masterRequest cluster.RelayRequest
	master := &Runtime{
		NodeID: "master", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-edge": "edge-01"},
		LoadState: func() (cluster.State, error) { return state, nil },
		OpenPeer: func(_ context.Context, nodeID string, relay cluster.RelayRequest) (proxy.DuplexStream, error) {
			if nodeID != "edge-01" {
				t.Fatalf("node=%q", nodeID)
			}
			masterRequest = relay
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		Random:     bytes.NewReader([]byte("clientrt")),
	}
	stream, handled, err := master.RouteClientTCP(
		context.Background(), "alice", proxy.Target{Host: "example.org", Port: 443}, request,
	)
	if err != nil || !handled || stream == nil || masterRequest.RouteID != request.RouteID {
		t.Fatalf("stream=%v handled=%v relay=%+v err=%v", stream, handled, masterRequest, err)
	}

	var forwarded cluster.RelayRequest
	edge := &Runtime{
		NodeID: "edge-01", MasterNodeID: "master", PeerPrincipals: map[string]string{"peer-master": "master"},
		OpenPeer: func(_ context.Context, nodeID string, relay cluster.RelayRequest) (proxy.DuplexStream, error) {
			if nodeID != "master" {
				t.Fatalf("edge master=%q", nodeID)
			}
			forwarded = relay
			return &nopDuplex{}, nil
		},
		DialTarget: func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		DialUDP:    func(context.Context, proxy.Target) (proxy.DuplexStream, error) { return &nopDuplex{}, nil },
		Random:     bytes.NewReader([]byte("edgehint")),
	}
	if _, handled, err := edge.RouteClientUDP(
		context.Background(), "alice", proxy.Target{Host: "203.0.113.20", Port: 443}, request,
	); err != nil || !handled || forwarded.ClientRoute == nil || forwarded.ClientRoute.RouteID != request.RouteID ||
		forwarded.Protocol != cluster.ProtocolUDP {
		t.Fatalf("forwarded=%+v handled=%v err=%v", forwarded, handled, err)
	}

	request.Action.NodeIDs = []string{"not-allowed"}
	if _, _, err := master.RouteClientTCP(
		context.Background(), "alice", proxy.Target{Host: "example.org", Port: 443}, request,
	); !errors.Is(err, cluster.ErrClientRouteUnauthorized) {
		t.Fatalf("unauthorized route error=%v", err)
	}
}

func relayState() cluster.State {
	now := time.Now().UTC()
	return cluster.State{
		Version: cluster.StateVersion, ClusterID: "cluster-01", Revision: 1, UpdatedAt: now,
		Nodes: []cluster.Node{
			{ID: "master", Name: "Master", Region: "Moscow", Roles: []cluster.NodeRole{cluster.RoleMaster, cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress}, PublicIdentity: "vpn.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "vpn.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "master-control", HostKeySHA256: "SHA256:master", ProvisionedAt: now, UpdatedAt: now},
			{ID: "edge-01", Name: "Edge", Region: "Finland", Roles: []cluster.NodeRole{cluster.RoleEgress}, PublicIdentity: "edge.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "edge.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "peer-edge", HostKeySHA256: "SHA256:edge", ProvisionedAt: now, UpdatedAt: now},
		},
		Routes: []cluster.Route{{ID: "media", Name: "Media", Priority: 10, Enabled: true, Source: cluster.RouteSourceAdmin, Match: cluster.RouteMatch{DomainSuffixes: []string{"youtube.com"}, Protocols: []cluster.NetworkProtocol{cluster.ProtocolTCP}}, Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}}}},
		Access: []cluster.UserAccess{{UserID: "alice", AllowedNodeIDs: []string{"master", "edge-01"}, AllowedRouteIDs: []string{"media"}, Revision: 1}},
	}
}

type nopDuplex struct{ bytes.Buffer }

func (*nopDuplex) Close() error             { return nil }
func (*nopDuplex) CloseWrite() error        { return nil }
func (*nopDuplex) Read([]byte) (int, error) { return 0, io.EOF }
