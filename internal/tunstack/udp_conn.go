package tunstack

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

type udpPacketConnection struct {
	association udpAssociation
	local       net.Addr
	remote      net.Addr
	dns         *dnsAttribution
	observeDNS  bool

	deadlineMu sync.Mutex
	deadline   *time.Timer
	closeOnce  sync.Once
	closeErr   error
}

type udpAssociation interface {
	ReadDatagram() ([]byte, np2proxy.Target, error)
	WriteDatagram([]byte, *np2proxy.Target) error
	Close() error
	Abort() error
}

var _ net.PacketConn = (*udpPacketConnection)(nil)

func (c *udpPacketConnection) ReadFrom(destination []byte) (int, net.Addr, error) {
	payload, source, err := c.association.ReadDatagram()
	if err != nil {
		return 0, nil, err
	}
	address, err := netip.ParseAddr(source.Host)
	if err != nil {
		return 0, nil, ErrInvalidMetadata
	}
	sourceAddress := net.UDPAddrFromAddrPort(netip.AddrPortFrom(address.Unmap(), source.Port))
	length := copy(destination, payload)
	if length != len(payload) {
		return length, sourceAddress, io.ErrShortBuffer
	}
	if c.observeDNS && c.dns != nil {
		c.dns.observeResponse(payload)
	}
	return length, sourceAddress, nil
}

func (c *udpPacketConnection) WriteTo(payload []byte, destination net.Addr) (int, error) {
	if destination != nil && !sameUDPAddress(destination, c.remote) {
		return 0, ErrInvalidMetadata
	}
	// Register the query before the association write. A fast DNS response may
	// be delivered by another goroutine before WriteDatagram returns; recording
	// it afterwards loses the only trustworthy domain attribution for the TUN
	// flow and makes domain/GeoSite routing silently fall back to the current
	// node.
	if c.observeDNS && c.dns != nil {
		c.dns.observeQuery(payload)
	}
	if err := c.association.WriteDatagram(payload, nil); err != nil {
		if c.observeDNS && c.dns != nil {
			c.dns.discardQuery(payload)
		}
		return 0, err
	}
	return len(payload), nil
}

func (c *udpPacketConnection) Close() error {
	c.closeOnce.Do(func() {
		c.deadlineMu.Lock()
		if c.deadline != nil {
			c.deadline.Stop()
			c.deadline = nil
		}
		c.deadlineMu.Unlock()
		c.closeErr = errors.Join(c.association.Close(), c.association.Abort())
	})
	return c.closeErr
}

func (c *udpPacketConnection) LocalAddr() net.Addr { return c.local }

func (c *udpPacketConnection) SetDeadline(deadline time.Time) error {
	return c.setCloseDeadline(deadline)
}

func (c *udpPacketConnection) SetReadDeadline(deadline time.Time) error {
	return c.setCloseDeadline(deadline)
}

func (c *udpPacketConnection) SetWriteDeadline(deadline time.Time) error {
	return c.setCloseDeadline(deadline)
}

func (c *udpPacketConnection) setCloseDeadline(deadline time.Time) error {
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

func sameUDPAddress(left, right net.Addr) bool {
	leftUDP, leftOK := left.(*net.UDPAddr)
	rightUDP, rightOK := right.(*net.UDPAddr)
	if !leftOK || !rightOK {
		return left.String() == right.String()
	}
	return leftUDP.AddrPort() == rightUDP.AddrPort()
}
