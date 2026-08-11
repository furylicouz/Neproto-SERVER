package tunstack

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	tunproxy "github.com/xjasonlyu/tun2socks/v2/proxy"

	np2proxy "neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

var (
	ErrInvalidMetadata = errors.New("invalid TUN flow metadata")
	ErrUDPUnsupported  = errors.New("NP/2 UDP flows are not supported")
)

type streamConnection interface {
	io.ReadWriteCloser
	CloseWrite() error
}

type streamOpenFunc func(context.Context, []byte) (streamConnection, error)

type udpOpenFunc func(
	context.Context, []byte,
) (streamConnection, *session.DatagramEndpoint, uint64, error)

// Dialer converts userspace TCP flows directly into NP/2 logical streams.
// It intentionally does not implement or connect to a SOCKS endpoint.
type Dialer struct {
	open                  streamOpenFunc
	prepareOpen           func() (streamOpenFunc, error)
	openUDP               udpOpenFunc
	rejectUDP             func(uint16) bool
	datagrams             *session.DatagramMux
	dns                   *dnsAttribution
	sniffDomains          bool
	firstFlightDomainHits atomic.Uint64
	firstFlightFallbacks  atomic.Uint64
	udpMaxPayload         uint64
	udpOpenTimeout        time.Duration
}

var _ tunproxy.Dialer = (*Dialer)(nil)

func NewDialer(mux *session.Mux) (*Dialer, error) {
	return newDialer(mux, 0)
}

func NewDialerWithUDP(mux *session.Mux, maxPayload uint64) (*Dialer, error) {
	if maxPayload < 1200 || maxPayload > np2proxy.MaxUDPDatagramPayload {
		return nil, session.ErrInvalidConfig
	}
	return newDialer(mux, maxPayload)
}

func NewDialerWithUDPFast(mux *session.Mux, maxPayload uint64, datagrams *session.DatagramMux) (*Dialer, error) {
	if datagrams == nil || !datagrams.Enabled() {
		return nil, session.ErrInvalidConfig
	}
	dialer, err := NewDialerWithUDP(mux, maxPayload)
	if err != nil {
		return nil, err
	}
	dialer.datagrams = datagrams
	return dialer, nil
}

func NewDialerWithSessionRouter(router *SessionRouter) (*Dialer, error) {
	return newDialerWithSessionRouter(router)
}

func newDialerWithSessionRouter(router *SessionRouter) (*Dialer, error) {
	if _, err := router.snapshot(); err != nil {
		return nil, err
	}
	return &Dialer{
		open: router.openStream, prepareOpen: router.pinStreamOpener,
		openUDP: router.openUDP,
		dns:     newDNSAttribution(time.Now), sniffDomains: true,
		udpOpenTimeout: 10 * time.Second,
	}, nil
}

func newDialer(mux *session.Mux, maxPayload uint64) (*Dialer, error) {
	if mux == nil {
		return nil, session.ErrInvalidConfig
	}
	return &Dialer{
		open: func(ctx context.Context, metadata []byte) (streamConnection, error) {
			return mux.Open(ctx, metadata)
		},
		dns: newDNSAttribution(time.Now), sniffDomains: true,
		udpMaxPayload: maxPayload, udpOpenTimeout: 10 * time.Second,
	}, nil
}

func (d *Dialer) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	if ctx == nil || d == nil || d.open == nil || metadata == nil || metadata.Network != M.TCP ||
		!metadata.DstIP.IsValid() || metadata.DstIP.IsUnspecified() || metadata.DstPort == 0 {
		return nil, ErrInvalidMetadata
	}
	opener := d.open
	if d.prepareOpen != nil {
		var err error
		opener, err = d.prepareOpen()
		if err != nil {
			return nil, err
		}
	}
	host, attributed := d.attributedHost(metadata.DstIP)
	local := tcpAddress(metadata.SrcIP, metadata.SrcPort)
	remote := tcpAddress(metadata.DstIP, metadata.DstPort)
	if d.sniffDomains && !attributed && (metadata.DstPort == 80 || metadata.DstPort == 443) {
		return newDeferredStreamConn(
			opener,
			np2proxy.Target{Host: host, Port: metadata.DstPort},
			local,
			remote,
			d.observeFirstFlight,
		), nil
	}
	target, err := np2proxy.EncodeTarget(np2proxy.Target{Host: host, Port: metadata.DstPort})
	if err != nil {
		return nil, errors.Join(ErrInvalidMetadata, err)
	}
	stream, err := opener(ctx, target)
	if err != nil {
		return nil, err
	}
	return &streamConn{
		stream: stream,
		local:  local,
		remote: remote,
	}, nil
}

func (d *Dialer) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	if d == nil || d.open == nil || metadata == nil || metadata.Network != M.UDP ||
		!metadata.DstIP.IsValid() || metadata.DstIP.IsUnspecified() || metadata.DstPort == 0 {
		return nil, ErrInvalidMetadata
	}
	if d.openUDP == nil && d.udpMaxPayload == 0 {
		return nil, ErrUDPUnsupported
	}
	target := np2proxy.Target{Host: d.targetHost(metadata.DstIP), Port: metadata.DstPort}
	request, err := np2proxy.EncodeOpenRequest(np2proxy.OpenRequest{
		Command: np2proxy.CommandUDPFixed, Target: target,
	})
	if err != nil {
		return nil, errors.Join(ErrInvalidMetadata, err)
	}
	timeout := d.udpOpenTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stream streamConnection
	var endpoint *session.DatagramEndpoint
	maximumPayload := d.udpMaxPayload
	if d.openUDP != nil {
		stream, endpoint, maximumPayload, err = d.openUDP(ctx, request)
	} else {
		stream, err = d.open(ctx, request)
		if err == nil && d.datagrams != nil {
			identified, ok := stream.(interface{ ID() uint64 })
			if !ok || identified.ID() == 0 {
				_ = stream.Close()
				return nil, session.ErrInvalidConfig
			}
			endpoint, err = d.datagrams.OpenEndpoint(identified.ID())
		}
	}
	if err != nil {
		if stream != nil {
			_ = stream.Close()
		}
		return nil, err
	}
	var association *np2proxy.UDPAssociation
	if endpoint != nil {
		association, err = np2proxy.NewUDPAssociationWithDatagrams(
			stream, np2proxy.CommandUDPFixed, target, maximumPayload, endpoint,
		)
	} else {
		association, err = np2proxy.NewUDPAssociation(
			stream, np2proxy.CommandUDPFixed, target, maximumPayload,
		)
	}
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &udpPacketConnection{
		association: association,
		local:       udpAddress(metadata.SrcIP, metadata.SrcPort),
		remote:      udpAddress(metadata.DstIP, metadata.DstPort),
		dns:         d.dns,
		observeDNS:  metadata.DstPort == 53,
	}, nil
}

func (d *Dialer) targetHost(address netip.Addr) string {
	host, _ := d.attributedHost(address)
	return host
}

func (d *Dialer) attributedHost(address netip.Addr) (string, bool) {
	if d != nil && d.dns != nil {
		if domain, ok := d.dns.domainFor(address.Unmap()); ok {
			return domain, true
		}
	}
	return address.String(), false
}

func (d *Dialer) observeFirstFlight(domainFound bool) {
	if d == nil {
		return
	}
	if domainFound {
		d.firstFlightDomainHits.Add(1)
		return
	}
	d.firstFlightFallbacks.Add(1)
}

func tcpAddress(address netip.Addr, port uint16) net.Addr {
	if !address.IsValid() {
		return &net.TCPAddr{}
	}
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(address, port))
}

func udpAddress(address netip.Addr, port uint16) net.Addr {
	if !address.IsValid() {
		return &net.UDPAddr{}
	}
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, port))
}

type streamConn struct {
	stream streamConnection
	local  net.Addr
	remote net.Addr

	deadlineMu sync.Mutex
	deadline   *time.Timer
	closeOnce  sync.Once
	closeErr   error
}

var _ net.Conn = (*streamConn)(nil)

func (c *streamConn) Read(destination []byte) (int, error) { return c.stream.Read(destination) }
func (c *streamConn) Write(source []byte) (int, error)     { return c.stream.Write(source) }

func (c *streamConn) Close() error {
	c.closeOnce.Do(func() {
		c.deadlineMu.Lock()
		if c.deadline != nil {
			c.deadline.Stop()
			c.deadline = nil
		}
		c.deadlineMu.Unlock()
		c.closeErr = c.stream.Close()
	})
	return c.closeErr
}

func (c *streamConn) CloseWrite() error    { return c.stream.CloseWrite() }
func (c *streamConn) CloseRead() error     { return nil }
func (c *streamConn) LocalAddr() net.Addr  { return c.local }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

func (c *streamConn) SetDeadline(deadline time.Time) error {
	return c.setCloseDeadline(deadline)
}

func (c *streamConn) SetReadDeadline(deadline time.Time) error {
	return c.setCloseDeadline(deadline)
}

func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

func (c *streamConn) setCloseDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.deadline != nil {
		c.deadline.Stop()
		c.deadline = nil
	}
	if deadline.IsZero() {
		return nil
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		go c.Close()
		return nil
	}
	c.deadline = time.AfterFunc(delay, func() { _ = c.Close() })
	return nil
}
