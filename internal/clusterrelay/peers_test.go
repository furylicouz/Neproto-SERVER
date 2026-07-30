package clusterrelay

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

func TestPeerPoolRelaysTCPAndUDPOverNP2Mux(t *testing.T) {
	clientMux, serverMux := newRelayMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})

	runtime := &Runtime{
		NodeID: "edge", MasterNodeID: "master",
		PeerPrincipals: map[string]string{"peer-master": "master"},
		OpenPeer: func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error) {
			return nil, ErrPeerUnavailable
		},
		DialTarget: newTCPEchoStream,
		DialUDP:    newUDPEchoStream,
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- (proxy.Server{
			Mux: serverMux, CredentialID: "peer-master", ClusterRelay: runtime.HandleRelay,
		}).Serve(ctx)
	}()

	connectCalls := 0
	pool, err := NewPeerPool(map[string]config.Client{"edge": {}}, func(context.Context, config.Client) (*session.Authenticated, error) {
		connectCalls++
		return &session.Authenticated{Mux: clientMux}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	for _, networkProtocol := range []cluster.NetworkProtocol{cluster.ProtocolTCP, cluster.ProtocolUDP} {
		request := cluster.RelayRequest{
			Version: cluster.RelayVersion, RouteID: "media", UserID: "user-01", RemainingHops: 1,
			VisitedNodeIDs: []string{"master"}, TraceID: "0123456789abcdef",
			TargetHost: "example.org", TargetPort: 443, Protocol: networkProtocol,
		}
		stream, err := pool.Open(ctx, "edge", request)
		if err != nil {
			t.Fatalf("open %s relay: %v", networkProtocol, err)
		}
		if networkProtocol == cluster.ProtocolTCP {
			payload := []byte("np2-cluster-tcp")
			if _, err := stream.Write(payload); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, len(payload))
			if _, err := io.ReadFull(stream, response); err != nil || !bytes.Equal(response, payload) {
				t.Fatalf("TCP response=%q err=%v", response, err)
			}
		} else {
			association, err := proxy.NewUDPAssociation(
				stream, proxy.CommandUDPFixed, proxy.Target{Host: "example.org", Port: 443}, 4096,
			)
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("np2-cluster-udp")
			if err := association.WriteDatagram(payload, nil); err != nil {
				t.Fatal(err)
			}
			response, _, err := association.ReadDatagram()
			if err != nil || !bytes.Equal(response, payload) {
				t.Fatalf("UDP response=%q err=%v", response, err)
			}
			_ = association.Close()
		}
		_ = stream.Close()
	}
	if connectCalls != 1 {
		t.Fatalf("pooled authenticated sessions=%d, want 1", connectCalls)
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("relay server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay server did not stop")
	}
}

func TestPeerPoolFetchesClusterStateOverAuthenticatedNP2Mux(t *testing.T) {
	clientMux, serverMux := newRelayMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"cluster_id":"cluster-01","revision":9}`)
	go func() {
		_ = (proxy.Server{
			Mux: serverMux, CredentialID: "peer-edge",
			ClusterState: func(_ context.Context, credentialID string) ([]byte, error) {
				if credentialID != "peer-edge" {
					t.Errorf("credentialID=%q", credentialID)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()
	pool, err := NewPeerPool(map[string]config.Client{"master": {}}, func(context.Context, config.Client) (*session.Authenticated, error) {
		return &session.Authenticated{Mux: clientMux}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	got, err := pool.FetchState(ctx, "master")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("state=%s err=%v", got, err)
	}
}

func TestPeerPoolRunsGeoDataControlOverAuthenticatedNP2Mux(t *testing.T) {
	clientMux, serverMux := newRelayMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	want := []byte(`{"version":1,"node_id":"edge","state":"ready"}`)
	go func() {
		_ = (proxy.Server{
			Mux: serverMux, CredentialID: "peer-master",
			GeoDataControl: func(_ context.Context, credentialID string, request cluster.GeoDataRequest) ([]byte, error) {
				if credentialID != "peer-master" || request.Operation != cluster.GeoDataUpdate {
					t.Errorf("unexpected request: %s %+v", credentialID, request)
				}
				return append([]byte(nil), want...), nil
			},
		}).Serve(ctx)
	}()
	pool, err := NewPeerPool(map[string]config.Client{"edge": {}}, func(context.Context, config.Client) (*session.Authenticated, error) {
		return &session.Authenticated{Mux: clientMux}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	got, err := pool.GeoData(ctx, "edge", cluster.GeoDataRequest{Version: cluster.GeoDataControlVersion, Operation: cluster.GeoDataUpdate})
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("status=%s err=%v", got, err)
	}
}

func newTCPEchoStream(context.Context, proxy.Target) (proxy.DuplexStream, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer listener.Close()
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			_, _ = io.Copy(connection, connection)
		}
	}()
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		<-done
		return nil, err
	}
	return connection.(*net.TCPConn), nil
}

func newUDPEchoStream(ctx context.Context, target proxy.Target) (proxy.DuplexStream, error) {
	external, internal := newTestDuplexPair()
	association, err := proxy.NewUDPAssociation(internal, proxy.CommandUDPFixed, target, 4096)
	if err != nil {
		return nil, err
	}
	go func() {
		defer association.Abort()
		for {
			payload, _, readErr := association.ReadDatagram()
			if readErr != nil {
				return
			}
			if writeErr := association.WriteDatagram(payload, nil); writeErr != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return external, nil
}

type testDuplex struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	once   sync.Once
}

func newTestDuplexPair() (*testDuplex, *testDuplex) {
	leftReader, rightWriter := io.Pipe()
	rightReader, leftWriter := io.Pipe()
	return &testDuplex{reader: leftReader, writer: leftWriter},
		&testDuplex{reader: rightReader, writer: rightWriter}
}

func (stream *testDuplex) Read(payload []byte) (int, error)  { return stream.reader.Read(payload) }
func (stream *testDuplex) Write(payload []byte) (int, error) { return stream.writer.Write(payload) }
func (stream *testDuplex) CloseWrite() error                 { return stream.writer.Close() }
func (stream *testDuplex) Close() error {
	var result error
	stream.once.Do(func() { result = errorsJoin(stream.writer.Close(), stream.reader.Close()) })
	return result
}

func errorsJoin(left, right error) error {
	if left != nil {
		return left
	}
	return right
}

type relayMemoryCarrier struct {
	in    <-chan []byte
	out   chan<- []byte
	done  <-chan struct{}
	close func()
	once  sync.Once
}

var _ carrier.Carrier = (*relayMemoryCarrier)(nil)

func newRelayMemoryCarrierPair() (*relayMemoryCarrier, *relayMemoryCarrier) {
	leftToRight := make(chan []byte, 128)
	rightToLeft := make(chan []byte, 128)
	done := make(chan struct{})
	var once sync.Once
	closePair := func() { once.Do(func() { close(done) }) }
	return &relayMemoryCarrier{in: rightToLeft, out: leftToRight, done: done, close: closePair},
		&relayMemoryCarrier{in: leftToRight, out: rightToLeft, done: done, close: closePair}
}

func (carrier *relayMemoryCarrier) Send(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	select {
	case carrier.out <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-carrier.done:
		return io.EOF
	}
}

func (carrier *relayMemoryCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case raw := <-carrier.in:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-carrier.done:
		return nil, io.EOF
	}
}

func (carrier *relayMemoryCarrier) Close() error {
	carrier.once.Do(carrier.close)
	return nil
}

func (*relayMemoryCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }

func newRelayMuxPair(t *testing.T) (*session.Mux, *session.Mux) {
	t.Helper()
	left, right := newRelayMemoryCarrierPair()
	typeMap, err := protocol.NewTypeMap([32]byte{0x4a, 0x71})
	if err != nil {
		t.Fatal(err)
	}
	client, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: left, TypeMap: typeMap, InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: right, TypeMap: typeMap, InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}
