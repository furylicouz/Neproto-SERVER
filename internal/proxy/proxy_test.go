package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

func TestCatalogClientReceivesBoundedAuthenticatedServerCatalog(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"cluster_id":"cluster-01"}`)
	go func() {
		_ = (Server{
			Mux: serverMux, CredentialID: "alice",
			Catalog: func(_ context.Context, credentialID string) ([]byte, error) {
				if credentialID != "alice" {
					t.Errorf("credentialID = %q", credentialID)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()

	got, err := FetchCatalog(ctx, clientMux)
	if err != nil {
		t.Fatalf("FetchCatalog() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("catalog = %s, want %s", got, want)
	}
}

func TestCatalogCommandFailsClosedWithoutHandler(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	go func() { _ = (Server{Mux: serverMux}).Serve(ctx) }()
	if _, err := FetchCatalog(ctx, clientMux); err == nil {
		t.Fatal("catalog request succeeded without server handler")
	}
}

func TestClusterStateRequiresAuthenticatedPeerHandler(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"cluster_id":"cluster-01","revision":7}`)
	go func() {
		_ = (Server{
			Mux: serverMux, CredentialID: "peer-edge",
			ClusterState: func(_ context.Context, credentialID string) ([]byte, error) {
				if credentialID != "peer-edge" {
					t.Errorf("credentialID = %q", credentialID)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()

	got, err := FetchClusterState(ctx, clientMux)
	if err != nil {
		t.Fatalf("FetchClusterState() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("state = %s, want %s", got, want)
	}
}

func TestClusterStateFailsClosedWithoutHandler(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	go func() { _ = (Server{Mux: serverMux, CredentialID: "peer-edge"}).Serve(ctx) }()
	if _, err := FetchClusterState(ctx, clientMux); err == nil {
		t.Fatal("cluster state request succeeded without server handler")
	}
}

func TestRelayedCatalogRequiresPeerAndPreservesOriginalUser(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"user_id":"iphone-user"}`)
	go func() {
		_ = (Server{
			Mux: serverMux, CredentialID: "peer-edge",
			CatalogRelay: func(_ context.Context, peerID, userID string) ([]byte, error) {
				if peerID != "peer-edge" || userID != "iphone-user" {
					t.Errorf("peer/user = %q/%q", peerID, userID)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()
	got, err := FetchRelayedCatalog(ctx, clientMux, "iphone-user")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("relayed catalog=%s err=%v", got, err)
	}
}

func TestCredentialSyncAcknowledgesOnlyAfterHandlerCommit(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	committed := make(chan struct{}, 1)
	go func() {
		_ = (Server{
			Mux: serverMux, CredentialID: "peer-master",
			CredentialSync: func(_ context.Context, peerID string, request cluster.CredentialSyncRequest) error {
				if peerID != "peer-master" || request.Operation != cluster.CredentialSyncRevoke {
					t.Errorf("unexpected sync request: %s %+v", peerID, request)
				}
				committed <- struct{}{}
				return nil
			},
		}).Serve(ctx)
	}()
	request := cluster.CredentialSyncRequest{Version: 1, Operation: cluster.CredentialSyncRevoke, CredentialID: "AQEBAQEBAQEBAQEBAQEBAQ"}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCredentialSync, CredentialSync: &request})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err := io.ReadFull(stream, ack[:]); err != nil || ack[0] != 1 {
		t.Fatalf("ack=%v err=%v", ack, err)
	}
	select {
	case <-committed:
	default:
		t.Fatal("acknowledgement arrived before credential commit")
	}
}

func TestGeoDataControlRequiresAuthenticatedHandlerAndReturnsStatus(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"node_id":"edge","state":"ready"}`)
	go func() {
		_ = (Server{
			Mux: serverMux, CredentialID: "peer-master",
			GeoDataControl: func(_ context.Context, peerID string, request cluster.GeoDataRequest) ([]byte, error) {
				if peerID != "peer-master" || request.Operation != cluster.GeoDataUpdate {
					t.Errorf("unexpected request: %s %+v", peerID, request)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()
	got, err := FetchGeoDataControl(ctx, clientMux, cluster.GeoDataRequest{Version: cluster.GeoDataControlVersion, Operation: cluster.GeoDataUpdate})
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("status=%s err=%v", got, err)
	}
}

func TestConnectorAndServerReachLocalTargetWithExplicitOverride(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer listener.Close()
	targetDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_, acceptErr = io.Copy(connection, connection)
			_ = connection.Close()
		}
		targetDone <- acceptErr
	}()
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- (Server{Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true}}).Serve(ctx)
	}()

	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	stream, err := (Connector{Mux: clientMux}).Connect(ctx, socks5.Request{Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatalf("connect target: %v", err)
	}
	payload := bytes.Repeat([]byte("np2-target"), 1024)
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write target: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, response); err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatal("target payload mismatch")
	}
	_ = stream.Close()
	cancel()
	select {
	case err := <-proxyDone:
		if err != nil {
			t.Fatalf("serve proxy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy server did not stop")
	}
	select {
	case <-targetDone:
	case <-time.After(time.Second):
		t.Fatal("target handler did not stop")
	}
}

func TestServerBlocksLoopbackBeforeDial(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	dialer := &recordingDialer{}
	go func() { _ = (Server{Mux: serverMux, Dialer: dialer}).Serve(ctx) }()

	_, err := (Connector{Mux: clientMux}).Connect(ctx, socks5.Request{Host: "127.0.0.1", Port: 80})
	var reply *socks5.ReplyError
	if !errors.As(err, &reply) || reply.Code != socks5.ReplyNotAllowed {
		t.Fatalf("expected policy rejection, got %v", err)
	}
	if dialer.Calls() != 0 {
		t.Fatal("policy-rejected destination was dialed")
	}
}

func TestServerMapsConnectionRefused(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	dialer := &recordingDialer{err: syscall.ECONNREFUSED}
	go func() {
		_ = (Server{Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true}, Dialer: dialer}).Serve(ctx)
	}()

	_, err := (Connector{Mux: clientMux}).Connect(ctx, socks5.Request{Host: "127.0.0.1", Port: 9})
	var reply *socks5.ReplyError
	if !errors.As(err, &reply) || reply.Code != socks5.ReplyConnectionRefused {
		t.Fatalf("expected connection refused, got %v", err)
	}
	if dialer.Calls() != 1 {
		t.Fatalf("dial calls=%d", dialer.Calls())
	}
}

func TestServerRelaysReliableUDPFixedAssociation(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP target: %v", err)
	}
	defer target.Close()
	targetDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, readErr = target.WriteToUDP(buffer[:length], source)
		}
		targetDone <- readErr
	}()
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: time.Second,
		}).Serve(ctx)
	}()

	targetAddress := target.LocalAddr().(*net.UDPAddr)
	metadata, err := EncodeOpenRequest(OpenRequest{
		Command: CommandUDPFixed,
		Target:  Target{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatalf("open UDP association: %v", err)
	}
	association, err := NewUDPAssociation(
		stream, CommandUDPFixed,
		Target{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)}, 4096,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("np2 reliable UDP echo")
	if err := association.WriteDatagram(payload, nil); err != nil {
		t.Fatalf("write UDP datagram: %v", err)
	}
	response, source, err := association.ReadDatagram()
	if err != nil {
		t.Fatalf("read UDP response: %v", err)
	}
	if !bytes.Equal(response, payload) || source.Host != targetAddress.IP.String() ||
		source.Port != uint16(targetAddress.Port) {
		t.Fatalf("response=%q source=%+v", response, source)
	}
	_ = association.Close()
	select {
	case err := <-targetDone:
		if err != nil {
			t.Fatalf("UDP target: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UDP target did not receive datagram")
	}
	cancel()
	select {
	case err := <-proxyDone:
		if err != nil {
			t.Fatalf("serve proxy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy server did not stop")
	}
}

func TestServerRelaysReliableUDPUnboundAndChecksEveryTarget(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = target.WriteToUDP(buffer[:length], source)
		}
	}()
	go func() {
		_ = (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: time.Second,
		}).Serve(ctx)
	}()
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandUDPAssociate})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatalf("open association: %v", err)
	}
	association, err := NewUDPAssociation(stream, CommandUDPAssociate, Target{}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	destination := Target{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)}
	if err := association.WriteDatagram([]byte("unbound"), &destination); err != nil {
		t.Fatal(err)
	}
	payload, source, err := association.ReadDatagram()
	if err != nil || string(payload) != "unbound" || source != destination {
		t.Fatalf("payload=%q source=%+v err=%v", payload, source, err)
	}
	_ = association.Close()
}

func TestServerRoutesFixedUDPThroughClusterRouter(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = target.WriteToUDP(buffer[:length], source)
		}
	}()
	var routeCalls atomic.Int64
	go func() {
		_ = (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			CredentialID: "user-01", MaxUDPPayload: 4096, UDPIdleTimeout: time.Second,
			RouteUDP: func(_ context.Context, userID string, candidate Target) (DuplexStream, bool, error) {
				if userID != "user-01" || candidate.Port == 0 {
					t.Errorf("route user=%q target=%+v", userID, candidate)
				}
				routeCalls.Add(1)
				return nil, false, nil
			},
		}).Serve(ctx)
	}()
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	destination := Target{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandUDPFixed, Target: destination})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatal(err)
	}
	association, err := NewUDPAssociation(stream, CommandUDPFixed, destination, 4096)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("cluster-routed-udp")
	if err := association.WriteDatagram(payload, nil); err != nil {
		t.Fatal(err)
	}
	response, _, err := association.ReadDatagram()
	if err != nil || !bytes.Equal(response, payload) || routeCalls.Load() != 1 {
		t.Fatalf("response=%q route_calls=%d err=%v", response, routeCalls.Load(), err)
	}
	_ = association.Close()
}

func TestConnectorExposesSOCKSUDPAssociationOverNP2(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = target.WriteToUDP(buffer[:length], source)
		}
	}()
	go func() {
		_ = (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: time.Second,
		}).Serve(ctx)
	}()
	association, err := (Connector{Mux: clientMux, MaxUDPPayload: 4096}).AssociateUDP(ctx)
	if err != nil {
		t.Fatalf("open SOCKS UDP association: %v", err)
	}
	defer association.Close()
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	destination := socks5.Request{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)}
	payload := []byte("desktop UDP through NP/2")
	if err := association.WriteDatagram(payload, destination); err != nil {
		t.Fatal(err)
	}
	response, source, err := association.ReadDatagram()
	if err != nil || source != destination || !bytes.Equal(response, payload) {
		t.Fatalf("response=%q source=%+v error=%v", response, source, err)
	}
}

func TestServerRejectsEveryUnboundUDPTargetByPolicy(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	go func() {
		_ = (Server{Mux: serverMux, MaxUDPPayload: 4096, UDPIdleTimeout: time.Second}).Serve(ctx)
	}()
	metadata, _ := EncodeOpenRequest(OpenRequest{Command: CommandUDPAssociate})
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatalf("open association: %v", err)
	}
	association, err := NewUDPAssociation(stream, CommandUDPAssociate, Target{}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	blocked := Target{Host: "127.0.0.1", Port: 53}
	if err := association.WriteDatagram([]byte("blocked"), &blocked); err != nil {
		t.Fatal(err)
	}
	_, _, err = association.ReadDatagram()
	var remoteError *UDPRemoteError
	if !errors.As(err, &remoteError) || remoteError.Code != UDPErrorPolicyDenied {
		t.Fatalf("policy error=%v", err)
	}
	_ = association.Close()
}

func TestServerLimitsConcurrentUDPAssociationsAndAccountsStats(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	metadata, err := EncodeOpenRequest(OpenRequest{
		Command: CommandUDPFixed,
		Target: Target{
			Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statistics := &UDPStatistics{}
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: 2 * time.Second,
			MaxUDPAssociations: 1, UDPStats: statistics,
		}).Serve(ctx)
	}()

	first, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatalf("open first UDP association: %v", err)
	}
	_, err = clientMux.Open(ctx, metadata)
	var rejection *session.RejectError
	if !errors.As(err, &rejection) || rejection.Code != socks5.ReplyGeneralFailure {
		t.Fatalf("second association rejection=%v", err)
	}
	waitForUDPStats(t, statistics, func(snapshot UDPStatsSnapshot) bool {
		return snapshot.ActiveAssociations == 1 && snapshot.OpenedAssociations == 1 &&
			snapshot.AssociationLimitRejects == 1
	})
	_ = first.Close()
	cancel()
	select {
	case err := <-proxyDone:
		if err != nil {
			t.Fatalf("serve proxy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy server did not stop")
	}
	if snapshot := statistics.Snapshot(); snapshot.ActiveAssociations != 0 {
		t.Fatalf("active associations after stop=%d", snapshot.ActiveAssociations)
	}
}

func TestServerAccountsReliableUDPTrafficWithoutDestinations(t *testing.T) {
	clientMux, serverMux := newProxyMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buffer := make([]byte, 4096)
		length, source, readErr := target.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = target.WriteToUDP(buffer[:length], source)
		}
	}()
	statistics := &UDPStatistics{}
	go func() {
		_ = (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true},
			MaxUDPPayload: 4096, UDPIdleTimeout: time.Second, UDPStats: statistics,
		}).Serve(ctx)
	}()
	targetAddress := target.LocalAddr().(*net.UDPAddr)
	metadata, _ := EncodeOpenRequest(OpenRequest{
		Command: CommandUDPFixed,
		Target:  Target{Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port)},
	})
	stream, err := clientMux.Open(ctx, metadata)
	if err != nil {
		t.Fatal(err)
	}
	association, err := NewUDPAssociation(stream, CommandUDPFixed, Target{
		Host: targetAddress.IP.String(), Port: uint16(targetAddress.Port),
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("stats do not contain this payload or its target")
	if err := association.WriteDatagram(payload, nil); err != nil {
		t.Fatal(err)
	}
	response, _, err := association.ReadDatagram()
	if err != nil || !bytes.Equal(response, payload) {
		t.Fatalf("response=%q err=%v", response, err)
	}
	waitForUDPStats(t, statistics, func(snapshot UDPStatsSnapshot) bool {
		return snapshot.ClientDatagrams == 1 && snapshot.TargetDatagrams == 1 &&
			snapshot.ClientBytes == uint64(len(payload)) && snapshot.TargetBytes == uint64(len(payload))
	})
	_ = association.Close()
}

func waitForUDPStats(t *testing.T, statistics *UDPStatistics, condition func(UDPStatsSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition(statistics.Snapshot()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("UDP statistics condition not met: %+v", statistics.Snapshot())
}

type recordingDialer struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return nil, d.err
}

func (d *recordingDialer) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newProxyMuxPair(t *testing.T) (*session.Mux, *session.Mux) {
	t.Helper()
	left, right := newProxyMemoryCarrierPair()
	typeMap, err := protocol.NewTypeMap([32]byte{0x4a, 0x71})
	if err != nil {
		t.Fatalf("type map: %v", err)
	}
	client, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: left, TypeMap: typeMap, InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatalf("client mux: %v", err)
	}
	server, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: right, TypeMap: typeMap, InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatalf("server mux: %v", err)
	}
	return client, server
}

type proxyMemoryCarrier struct {
	in    <-chan []byte
	out   chan<- []byte
	done  <-chan struct{}
	close func()
	once  sync.Once
}

var _ carrier.Carrier = (*proxyMemoryCarrier)(nil)

func newProxyMemoryCarrierPair() (*proxyMemoryCarrier, *proxyMemoryCarrier) {
	leftToRight := make(chan []byte, 128)
	rightToLeft := make(chan []byte, 128)
	done := make(chan struct{})
	var once sync.Once
	closePair := func() { once.Do(func() { close(done) }) }
	return &proxyMemoryCarrier{in: rightToLeft, out: leftToRight, done: done, close: closePair},
		&proxyMemoryCarrier{in: leftToRight, out: rightToLeft, done: done, close: closePair}
}

func (c *proxyMemoryCarrier) Send(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	select {
	case c.out <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.EOF
	}
}

func (c *proxyMemoryCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case raw := <-c.in:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *proxyMemoryCarrier) Close() error {
	c.once.Do(c.close)
	return nil
}

func (c *proxyMemoryCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }
