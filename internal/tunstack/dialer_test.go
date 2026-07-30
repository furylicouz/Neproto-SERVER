package tunstack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

func TestDialerOpensCanonicalNP2Targets(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		port uint16
	}{
		{name: "IPv4", ip: "203.0.113.41", port: 443},
		{name: "IPv6", ip: "2001:db8:1234::9", port: 8443},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var opened []byte
			dialer := &Dialer{open: func(_ context.Context, metadata []byte) (streamConnection, error) {
				opened = append([]byte(nil), metadata...)
				return &stubStream{}, nil
			}}
			destination := netip.MustParseAddr(test.ip)
			connection, err := dialer.DialContext(context.Background(), &M.Metadata{
				Network: M.TCP, DstIP: destination, DstPort: test.port,
				SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49152,
			})
			if err != nil {
				t.Fatalf("dial NP/2 target: %v", err)
			}
			defer connection.Close()
			target, err := np2proxy.DecodeTarget(opened)
			if err != nil {
				t.Fatalf("decode opened target: %v", err)
			}
			if target.Host != test.ip || target.Port != test.port {
				t.Fatalf("target=%s:%d, want %s:%d", target.Host, target.Port, test.ip, test.port)
			}
			if got := connection.RemoteAddr().String(); got != netip.AddrPortFrom(destination, test.port).String() {
				t.Fatalf("remote address=%q", got)
			}
		})
	}
}

func TestDialerUsesTLSClientHelloSNIWhenDNSAttributionIsUnavailable(t *testing.T) {
	var opened []byte
	dialer := &Dialer{
		open: func(_ context.Context, metadata []byte) (streamConnection, error) {
			opened = append([]byte(nil), metadata...)
			return &stubStream{}, nil
		},
		dns: newDNSAttribution(time.Now), sniffDomains: true,
	}
	connection, err := dialer.DialContext(context.Background(), &M.Metadata{
		Network: M.TCP, DstIP: netip.MustParseAddr("198.51.100.27"), DstPort: 443,
		SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49152,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if opened != nil {
		t.Fatal("numeric NP/2 target opened before the first TLS flight")
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := connection.Read(make([]byte, 1))
		readDone <- readErr
	}()
	time.Sleep(20 * time.Millisecond)
	if opened != nil {
		t.Fatal("concurrent response reader opened a numeric target before ClientHello")
	}
	clientHello := testTLSClientHello("chatgpt.com")
	split := len(clientHello) / 2
	if written, err := connection.Write(clientHello[:split]); err != nil || written != split {
		t.Fatalf("buffer first ClientHello fragment: written=%d err=%v", written, err)
	}
	if opened != nil {
		t.Fatal("incomplete ClientHello opened a numeric NP/2 target")
	}
	if written, err := connection.Write(clientHello[split:]); err != nil || written != len(clientHello)-split {
		t.Fatalf("write second ClientHello fragment: written=%d err=%v", written, err)
	}
	target, err := np2proxy.DecodeTarget(opened)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "chatgpt.com" || target.Port != 443 {
		t.Fatalf("target=%s:%d", target.Host, target.Port)
	}
	select {
	case readErr := <-readDone:
		if !errors.Is(readErr, io.EOF) {
			t.Fatalf("response reader error=%v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("response reader did not continue after deferred open")
	}
}

func TestDialerUsesHTTPHostAndFallsBackForUnknownFirstFlight(t *testing.T) {
	tests := []struct {
		name, payload, wantHost string
		port                    uint16
	}{
		{name: "HTTP host", payload: "GET / HTTP/1.1\r\nHost: api.openai.com\r\n\r\n", wantHost: "api.openai.com", port: 80},
		{name: "unknown", payload: "\x01\x02\x03\x04", wantHost: "198.51.100.28", port: 443},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var opened []byte
			dialer := &Dialer{
				open: func(_ context.Context, metadata []byte) (streamConnection, error) {
					opened = append([]byte(nil), metadata...)
					return &stubStream{}, nil
				},
				dns: newDNSAttribution(time.Now), sniffDomains: true,
			}
			connection, err := dialer.DialContext(context.Background(), &M.Metadata{
				Network: M.TCP, DstIP: netip.MustParseAddr("198.51.100.28"), DstPort: test.port,
				SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49153,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if _, err := connection.Write([]byte(test.payload)); err != nil {
				t.Fatal(err)
			}
			target, err := np2proxy.DecodeTarget(opened)
			if err != nil {
				t.Fatal(err)
			}
			if target.Host != test.wantHost {
				t.Fatalf("target host=%q, want %q", target.Host, test.wantHost)
			}
		})
	}
}

func testTLSClientHello(serverName string) []byte {
	name := []byte(serverName)
	serverNameEntry := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...)
	serverNameList := append([]byte{byte(len(serverNameEntry) >> 8), byte(len(serverNameEntry))}, serverNameEntry...)
	extension := append([]byte{0, 0, byte(len(serverNameList) >> 8), byte(len(serverNameList))}, serverNameList...)
	extensions := append([]byte{byte(len(extension) >> 8), byte(len(extension))}, extension...)
	body := append([]byte{3, 3}, make([]byte, 32)...)
	body = append(body, 0, 0, 2, 0x13, 0x01, 1, 0)
	body = append(body, extensions...)
	handshake := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return append([]byte{0x16, 3, 1, byte(len(handshake) >> 8), byte(len(handshake))}, handshake...)
}

func TestSessionRouterSwitchesNewFlowsWithoutBreakingExistingFlows(t *testing.T) {
	firstOpened := 0
	secondOpened := 0
	router := &SessionRouter{active: sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			firstOpened++
			return &taggedStream{tag: "first"}, nil
		},
		maxUDPPayload: 1200,
	}}
	dialer, err := newDialerWithSessionRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	metadata := &M.Metadata{
		Network: M.TCP, DstIP: netip.MustParseAddr("203.0.113.9"), DstPort: 443,
		SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49152,
	}
	firstConnection, err := dialer.DialContext(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.switchRoute(sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			secondOpened++
			return &taggedStream{tag: "second"}, nil
		},
		maxUDPPayload: 1200,
	}); err != nil {
		t.Fatal(err)
	}
	secondConnection, err := dialer.DialContext(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstConnection.Write([]byte("\x01")); err != nil {
		t.Fatal(err)
	}
	if _, err := secondConnection.Write([]byte("\x01")); err != nil {
		t.Fatal(err)
	}
	firstTag := firstConnection.(*deferredStreamConn).stream.stream.(*taggedStream).tag
	secondTag := secondConnection.(*deferredStreamConn).stream.stream.(*taggedStream).tag
	if firstTag != "first" || secondTag != "second" || firstOpened != 1 || secondOpened != 1 {
		t.Fatalf("tags=(%q,%q) opens=(%d,%d)", firstTag, secondTag, firstOpened, secondOpened)
	}
	if _, err := firstConnection.Write([]byte("still alive")); err != nil {
		t.Fatalf("existing flow broke after switch: %v", err)
	}
}

func TestSessionRouterSelectsLeastLoadedRouteAndRotatesTies(t *testing.T) {
	var opened []string
	makeRoute := func(tag string, active uint64) sessionRoute {
		return sessionRoute{
			open: func(context.Context, []byte) (streamConnection, error) {
				opened = append(opened, tag)
				return &taggedStream{tag: tag}, nil
			},
			activeStreams: func() uint64 { return active },
		}
	}
	router := &SessionRouter{}
	if err := router.switchRoute(makeRoute("primary", 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := router.addRoute(makeRoute("secondary-a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := router.addRoute(makeRoute("secondary-b", 1)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		stream, err := router.openStream(context.Background(), []byte("target"))
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Close()
	}
	want := []string{"secondary-a", "secondary-b", "secondary-a", "secondary-b"}
	if !slices.Equal(opened, want) {
		t.Fatalf("route assignments=%v, want %v", opened, want)
	}
	if healthy, assignments := router.PoolStats(); healthy != 3 || assignments != 4 {
		t.Fatalf("pool stats healthy=%d assignments=%d", healthy, assignments)
	}
}

func TestSessionRouterPinsUDPToPrimaryAndBoundsPool(t *testing.T) {
	primaryOpens := 0
	secondaryOpens := 0
	router := &SessionRouter{}
	if err := router.switchRoute(sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			primaryOpens++
			return &loopbackStream{}, nil
		},
		activeStreams: func() uint64 { return 9 }, maxUDPPayload: 1200,
	}); err != nil {
		t.Fatal(err)
	}
	secondary := sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			secondaryOpens++
			return &loopbackStream{}, nil
		},
		activeStreams: func() uint64 { return 0 }, maxUDPPayload: 1200,
	}
	secondaryID, err := router.addRoute(secondary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.addRoute(secondary); err != nil {
		t.Fatal(err)
	}
	if _, err := router.addRoute(secondary); !errors.Is(err, ErrCarrierPoolFull) {
		t.Fatalf("fourth carrier error=%v", err)
	}
	metadata, err := np2proxy.EncodeOpenRequest(np2proxy.OpenRequest{
		Command: np2proxy.CommandUDPFixed,
		Target:  np2proxy.Target{Host: "1.1.1.1", Port: 53},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, _, _, err := router.openUDP(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if primaryOpens != 1 || secondaryOpens != 0 {
		t.Fatalf("UDP opens primary=%d secondary=%d", primaryOpens, secondaryOpens)
	}
	if !router.removeRoute(secondaryID) {
		t.Fatal("secondary carrier was not removed")
	}
	if healthy, _ := router.PoolStats(); healthy != 2 {
		t.Fatalf("healthy pool after removal=%d, want 2", healthy)
	}
}

func TestSessionRouterPromotesSecondaryWithoutDiscardingHealthyPool(t *testing.T) {
	primaryOpens := 0
	secondaryOpens := 0
	router := &SessionRouter{}
	if err := router.switchRoute(sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			primaryOpens++
			return &loopbackStream{}, nil
		},
		activeStreams: func() uint64 { return 0 }, maxUDPPayload: 1200,
	}); err != nil {
		t.Fatal(err)
	}
	secondaryID, err := router.addRoute(sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			secondaryOpens++
			return &loopbackStream{}, nil
		},
		activeStreams: func() uint64 { return 0 }, maxUDPPayload: 1200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Promote(secondaryID); err != nil {
		t.Fatal(err)
	}
	metadata, err := np2proxy.EncodeOpenRequest(np2proxy.OpenRequest{
		Command: np2proxy.CommandUDPFixed,
		Target:  np2proxy.Target{Host: "1.1.1.1", Port: 53},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, _, _, err := router.openUDP(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if primaryOpens != 0 || secondaryOpens != 1 {
		t.Fatalf("promoted UDP opens primary=%d secondary=%d", primaryOpens, secondaryOpens)
	}
	if healthy, _ := router.PoolStats(); healthy != 2 {
		t.Fatalf("promotion discarded routes: healthy=%d", healthy)
	}
	if !router.Remove(1) {
		t.Fatal("old primary was not removable after promotion")
	}
}

func TestSessionRouterRejectsInvalidOrUDPDisabledRoute(t *testing.T) {
	router := &SessionRouter{}
	if err := router.switchRoute(sessionRoute{}); !errors.Is(err, ErrInvalidStackConfig) {
		t.Fatalf("invalid route error=%v", err)
	}
	router.active = sessionRoute{open: func(context.Context, []byte) (streamConnection, error) {
		return &loopbackStream{}, nil
	}}
	dialer, err := newDialerWithSessionRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialer.DialUDP(&M.Metadata{
		Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 53,
	}); !errors.Is(err, ErrUDPUnsupported) {
		t.Fatalf("UDP-disabled route error=%v", err)
	}
}

func TestSessionRouterForcesQUICToTCPOnReliableUDPRoute(t *testing.T) {
	opened := 0
	router := &SessionRouter{active: sessionRoute{
		open: func(context.Context, []byte) (streamConnection, error) {
			opened++
			return &loopbackStream{}, nil
		},
		maxUDPPayload: 1200,
	}}
	dialer, err := newDialerWithSessionRouter(router)
	if err != nil {
		t.Fatal(err)
	}

	_, err = dialer.DialUDP(&M.Metadata{
		Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 443,
	})
	if !errors.Is(err, ErrReliableQUICFallback) {
		t.Fatalf("UDP/443 error=%v, want reliable-carrier QUIC fallback", err)
	}
	if opened != 0 {
		t.Fatalf("UDP/443 opened %d NP/2 streams before fallback", opened)
	}
	if got := router.QUICFallbackCount(); got != 1 {
		t.Fatalf("QUIC fallback count=%d, want 1", got)
	}
	if got := router.UDPMode(); got != "reliable-stream-quic-fallback" {
		t.Fatalf("UDP mode=%q", got)
	}

	dns, err := dialer.DialUDP(&M.Metadata{
		Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 53,
	})
	if err != nil {
		t.Fatalf("ordinary UDP must remain available: %v", err)
	}
	defer dns.Close()
	if opened != 1 {
		t.Fatalf("ordinary UDP opened %d NP/2 streams, want 1", opened)
	}
}

func TestDialerRejectsUDPWithoutOpeningStream(t *testing.T) {
	called := false
	dialer := &Dialer{open: func(context.Context, []byte) (streamConnection, error) {
		called = true
		return nil, nil
	}}
	if _, err := dialer.DialUDP(&M.Metadata{
		Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 53,
	}); !errors.Is(err, ErrUDPUnsupported) {
		t.Fatalf("expected UDP unsupported error, got %v", err)
	}
	if called {
		t.Fatal("UDP rejection opened an NP/2 stream")
	}
}

func TestDialerOpensReliableNP2UDPAssociation(t *testing.T) {
	var opened []byte
	stream := &loopbackStream{}
	dialer := &Dialer{
		open: func(_ context.Context, metadata []byte) (streamConnection, error) {
			opened = append([]byte(nil), metadata...)
			return stream, nil
		},
		udpMaxPayload:  1200,
		udpOpenTimeout: time.Second,
	}
	destination := netip.MustParseAddr("1.1.1.1")
	packetConnection, err := dialer.DialUDP(&M.Metadata{
		Network: M.UDP, DstIP: destination, DstPort: 53,
		SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49152,
	})
	if err != nil {
		t.Fatalf("dial UDP: %v", err)
	}
	defer packetConnection.Close()
	request, err := np2proxy.DecodeOpenRequest(opened)
	if err != nil || request.Command != np2proxy.CommandUDPFixed ||
		request.Target != (np2proxy.Target{Host: "1.1.1.1", Port: 53}) {
		t.Fatalf("opened request=%+v err=%v", request, err)
	}
	payload := []byte("dns")
	remote := net.UDPAddrFromAddrPort(netip.AddrPortFrom(destination, 53))
	if _, err := packetConnection.WriteTo(payload, remote); err != nil {
		t.Fatalf("write UDP: %v", err)
	}
	buffer := make([]byte, 1200)
	length, source, err := packetConnection.ReadFrom(buffer)
	if err != nil || !bytes.Equal(buffer[:length], payload) || source.String() != remote.String() {
		t.Fatalf("length=%d source=%v payload=%q err=%v", length, source, buffer[:length], err)
	}
}

func TestDialerRejectsInvalidMetadata(t *testing.T) {
	dialer := &Dialer{open: func(context.Context, []byte) (streamConnection, error) {
		return &stubStream{}, nil
	}}
	for _, metadata := range []*M.Metadata{
		nil,
		{Network: M.UDP, DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 443},
		{Network: M.TCP, DstPort: 443},
		{Network: M.TCP, DstIP: netip.MustParseAddr("1.1.1.1")},
	} {
		if _, err := dialer.DialContext(context.Background(), metadata); !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("metadata=%#v error=%v", metadata, err)
		}
	}
}

type stubStream struct{}

func (*stubStream) Read([]byte) (int, error)    { return 0, io.EOF }
func (*stubStream) Write(p []byte) (int, error) { return len(p), nil }
func (*stubStream) Close() error                { return nil }
func (*stubStream) CloseWrite() error           { return nil }

type taggedStream struct {
	tag string
}

func (*taggedStream) Read([]byte) (int, error)          { return 0, io.EOF }
func (*taggedStream) Write(payload []byte) (int, error) { return len(payload), nil }
func (*taggedStream) Close() error                      { return nil }
func (*taggedStream) CloseWrite() error                 { return nil }

type loopbackStream struct {
	mu sync.Mutex
	bytes.Buffer
}

func (s *loopbackStream) Read(destination []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Buffer.Read(destination)
}

func (s *loopbackStream) Write(source []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Buffer.Write(source)
}

func (*loopbackStream) Close() error      { return nil }
func (*loopbackStream) CloseWrite() error { return nil }
