package webrtc

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	pion "github.com/pion/webrtc/v4"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const (
	defaultMessageQueue   = 64
	maxDatagramPayload    = 1150
	dualChannelReadyGrace = 250 * time.Millisecond
)

var (
	ErrInvalidConfig       = errors.New("invalid WebRTC carrier configuration")
	ErrMessageTooLarge     = errors.New("WebRTC carrier message too large")
	ErrUnexpectedMessage   = errors.New("WebRTC carrier requires binary messages")
	ErrBackpressure        = errors.New("WebRTC carrier receive queue full")
	ErrDatagramUnavailable = errors.New("WebRTC datagram channel unavailable")
	ErrDatagramTooLarge    = errors.New("WebRTC datagram too large")
	ErrClosed              = errors.New("WebRTC carrier closed")
)

type EngineConfig struct {
	UDPPortMin        uint16
	UDPPortMax        uint16
	DisconnectedAfter time.Duration
	FailedAfter       time.Duration
	KeepAliveInterval time.Duration
}

func newAPI(config EngineConfig) (*pion.API, error) {
	if (config.UDPPortMin == 0) != (config.UDPPortMax == 0) ||
		(config.UDPPortMin != 0 && config.UDPPortMin > config.UDPPortMax) ||
		config.DisconnectedAfter < 0 || config.FailedAfter < 0 || config.KeepAliveInterval < 0 {
		return nil, ErrInvalidConfig
	}
	disconnected := config.DisconnectedAfter
	if disconnected == 0 {
		disconnected = 5 * time.Second
	}
	failed := config.FailedAfter
	if failed == 0 {
		failed = 10 * time.Second
	}
	keepAlive := config.KeepAliveInterval
	if keepAlive == 0 {
		keepAlive = 2 * time.Second
	}

	settingEngine := pion.SettingEngine{}
	settingEngine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeUDP4, pion.NetworkTypeUDP6})
	settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settingEngine.SetICETimeouts(disconnected, failed, keepAlive)
	settingEngine.SetSCTPMaxMessageSize(uint32(protocol.MaxCellSize))
	settingEngine.SetSCTPMaxReceiveBufferSize(uint32(protocol.MaxCellSize * defaultMessageQueue))
	if config.UDPPortMin != 0 {
		if err := settingEngine.SetEphemeralUDPPortRange(config.UDPPortMin, config.UDPPortMax); err != nil {
			return nil, errors.Join(ErrInvalidConfig, err)
		}
	}
	return pion.NewAPI(pion.WithSettingEngine(settingEngine)), nil
}

type Conn struct {
	peer            *pion.PeerConnection
	dataChannel     *pion.DataChannel
	datagramChannel *pion.DataChannel
	messages        chan []byte
	datagrams       chan []byte
	ready           chan struct{}
	datagramReady   chan struct{}
	done            chan struct{}
	release         func()
	onOpen          func(*Conn)

	sendMu           sync.Mutex
	datagramSendMu   sync.Mutex
	datagramMu       sync.RWMutex
	errMu            sync.Mutex
	err              error
	openOnce         sync.Once
	datagramOpenOnce sync.Once
	onOpenOnce       sync.Once
	abortOnce        sync.Once
	finishOnce       sync.Once
}

var _ carrier.Carrier = (*Conn)(nil)
var _ carrier.DatagramCarrier = (*Conn)(nil)

func newConn(peer *pion.PeerConnection, dataChannel, datagramChannel *pion.DataChannel, release func(), onOpen func(*Conn)) *Conn {
	connection := &Conn{
		peer: peer, dataChannel: dataChannel,
		messages:  make(chan []byte, defaultMessageQueue),
		datagrams: make(chan []byte, defaultMessageQueue),
		ready:     make(chan struct{}), datagramReady: make(chan struct{}), done: make(chan struct{}),
		release: release, onOpen: onOpen,
	}
	dataChannel.OnOpen(func() {
		connection.openOnce.Do(func() {
			close(connection.ready)
			go connection.notifyOpen()
		})
	})
	dataChannel.OnMessage(func(message pion.DataChannelMessage) {
		if message.IsString {
			connection.abort(ErrUnexpectedMessage)
			return
		}
		if len(message.Data) > protocol.MaxCellSize {
			connection.abort(ErrMessageTooLarge)
			return
		}
		copyOfMessage := append([]byte(nil), message.Data...)
		select {
		case connection.messages <- copyOfMessage:
		case <-connection.done:
		default:
			connection.abort(ErrBackpressure)
		}
	})
	dataChannel.OnError(func(err error) { connection.abort(err) })
	dataChannel.OnClose(func() { connection.abort(io.EOF) })
	peer.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		switch state {
		case pion.PeerConnectionStateFailed:
			connection.abort(errors.New("WebRTC peer connection failed"))
		case pion.PeerConnectionStateClosed:
			connection.abort(io.EOF)
		}
	})
	if datagramChannel != nil {
		connection.attachDatagramChannel(datagramChannel)
	}
	return connection
}

func (c *Conn) attachDatagramChannel(dataChannel *pion.DataChannel) bool {
	if dataChannel == nil || dataChannel.Ordered() || dataChannel.MaxRetransmits() == nil ||
		*dataChannel.MaxRetransmits() != 0 || dataChannel.MaxPacketLifeTime() != nil {
		return false
	}
	c.datagramMu.Lock()
	if c.datagramChannel != nil {
		c.datagramMu.Unlock()
		return false
	}
	c.datagramChannel = dataChannel
	c.datagramMu.Unlock()
	markOpen := func() { c.datagramOpenOnce.Do(func() { close(c.datagramReady) }) }
	dataChannel.OnOpen(markOpen)
	if dataChannel.ReadyState() == pion.DataChannelStateOpen {
		markOpen()
	}
	dataChannel.OnMessage(func(message pion.DataChannelMessage) {
		if message.IsString || len(message.Data) > maxDatagramPayload {
			return
		}
		copyOfMessage := append([]byte(nil), message.Data...)
		select {
		case c.datagrams <- copyOfMessage:
		case <-c.done:
		default:
			// Unreliable traffic is dropped under pressure; it must not abort
			// the reliable WebRTC data channel.
		}
	})
	return true
}

func (c *Conn) notifyOpen() {
	timer := time.NewTimer(dualChannelReadyGrace)
	defer timer.Stop()
	select {
	case <-c.datagramReady:
	case <-timer.C:
	case <-c.done:
		return
	}
	c.onOpenOnce.Do(func() {
		if c.onOpen != nil {
			c.onOpen(c)
		}
	})
}

func (c *Conn) Send(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if len(raw) > protocol.MaxCellSize {
		return ErrMessageTooLarge
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.connectionError()
	default:
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.dataChannel.Send(raw); err != nil {
		c.abort(err)
		return err
	}
	return nil
}

func (c *Conn) Receive(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	select {
	case <-c.done:
		return nil, c.connectionError()
	default:
	}
	select {
	case raw := <-c.messages:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.connectionError()
	}
}

func (c *Conn) SendDatagram(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if len(raw) > maxDatagramPayload {
		return ErrDatagramTooLarge
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.datagramReady:
	case <-c.done:
		return c.connectionError()
	default:
		return ErrDatagramUnavailable
	}
	c.datagramMu.RLock()
	dataChannel := c.datagramChannel
	c.datagramMu.RUnlock()
	if dataChannel == nil {
		return ErrDatagramUnavailable
	}
	c.datagramSendMu.Lock()
	defer c.datagramSendMu.Unlock()
	if err := dataChannel.Send(raw); err != nil {
		return err
	}
	return nil
}

func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	select {
	case <-c.datagramReady:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.connectionError()
	}
	select {
	case raw := <-c.datagrams:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.connectionError()
	}
}

func (c *Conn) MaxDatagramPayload() int {
	select {
	case <-c.datagramReady:
		return maxDatagramPayload
	default:
		return 0
	}
}

func (c *Conn) Close() error {
	c.finish(ErrClosed)
	return nil
}

func (c *Conn) Kind() protocol.CarrierKind {
	return protocol.CarrierWebRTC
}

// RemoteAddresses returns the numeric address of the selected remote ICE
// candidate. Packet-tunnel clients use it to keep the live carrier outside
// the default route they install after the NP/2 handshake.
func (c *Conn) RemoteAddresses() []netip.Addr {
	sctp := c.peer.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return nil
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Remote == nil {
		return nil
	}
	address, err := netip.ParseAddr(pair.Remote.Address)
	if err != nil || !address.IsValid() {
		return nil
	}
	return []netip.Addr{address.Unmap()}
}

func (c *Conn) waitOpen(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.connectionError()
	}
}

func (c *Conn) finish(err error) {
	if err == nil {
		err = ErrClosed
	}
	c.finishOnce.Do(func() {
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		close(c.done)
		_ = c.dataChannel.Close()
		c.datagramMu.RLock()
		datagramChannel := c.datagramChannel
		c.datagramMu.RUnlock()
		if datagramChannel != nil {
			_ = datagramChannel.Close()
		}
		_ = c.peer.Close()
		if c.release != nil {
			c.release()
		}
	})
}

func (c *Conn) abort(err error) {
	c.abortOnce.Do(func() { go c.finish(err) })
}

func (c *Conn) connectionError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		return ErrClosed
	}
	return c.err
}
