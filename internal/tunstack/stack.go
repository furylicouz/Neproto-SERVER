package tunstack

import (
	"errors"
	"runtime"
	"strconv"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"neproto.local/chameleon/internal/session"
)

var (
	ErrInvalidStackConfig = errors.New("invalid NP/2 TUN stack configuration")
	ErrCarrierPoolFull    = errors.New("NP/2 carrier pool is full")
)

const (
	minimumMTU = 1280
	maximumMTU = 9000
)

// Stack owns a duplicated TUN file descriptor and the userspace TCP/IP stack
// attached to it. Closing Stack never closes NetworkExtension's original FD.
type Stack struct {
	device device.Device
	stack  *stack.Stack
	tunnel *tunnel.Tunnel
	dialer *Dialer
	once   sync.Once
}

func Start(fileDescriptor int, mtu uint32, mux *session.Mux) (*Stack, error) {
	return start(fileDescriptor, mtu, mux, 0)
}

func StartWithUDP(
	fileDescriptor int,
	mtu uint32,
	mux *session.Mux,
	maxUDPPayload uint64,
) (*Stack, error) {
	if maxUDPPayload < 1200 {
		return nil, ErrInvalidStackConfig
	}
	return start(fileDescriptor, mtu, mux, maxUDPPayload)
}

func StartWithUDPFast(
	fileDescriptor int,
	mtu uint32,
	mux *session.Mux,
	maxUDPPayload uint64,
	datagrams *session.DatagramMux,
) (*Stack, error) {
	if maxUDPPayload < 1200 || datagrams == nil || !datagrams.Enabled() {
		return nil, ErrInvalidStackConfig
	}
	return startWithDatagrams(fileDescriptor, mtu, mux, maxUDPPayload, datagrams)
}

func StartWithSessionRouter(fileDescriptor int, mtu uint32, router *SessionRouter) (*Stack, error) {
	if fileDescriptor < 0 || mtu < minimumMTU || mtu > maximumMTU || router == nil {
		return nil, ErrInvalidStackConfig
	}
	dialer, err := NewDialerWithSessionRouter(router)
	if err != nil {
		return nil, err
	}
	return startWithDialer(fileDescriptor, mtu, dialer)
}

// StartWithSessionRouterDevice attaches an already-created platform TUN device
// to the same direct NP/2 data plane used by NetworkExtension. The caller owns
// platform address and route setup; Stack owns and closes the device.
func StartWithSessionRouterDevice(endpoint device.Device, mtu uint32, router *SessionRouter) (*Stack, error) {
	if endpoint == nil || mtu < minimumMTU || mtu > maximumMTU || router == nil {
		return nil, ErrInvalidStackConfig
	}
	dialer, err := NewDialerWithSessionRouter(router)
	if err != nil {
		return nil, err
	}
	return startWithDevice(endpoint, dialer)
}

func start(fileDescriptor int, mtu uint32, mux *session.Mux, maxUDPPayload uint64) (*Stack, error) {
	return startWithDatagrams(fileDescriptor, mtu, mux, maxUDPPayload, nil)
}

func startWithDatagrams(fileDescriptor int, mtu uint32, mux *session.Mux, maxUDPPayload uint64, datagrams *session.DatagramMux) (*Stack, error) {
	if fileDescriptor < 0 || mtu < minimumMTU || mtu > maximumMTU || mux == nil {
		return nil, ErrInvalidStackConfig
	}
	var dialer *Dialer
	var err error
	if maxUDPPayload == 0 {
		dialer, err = NewDialer(mux)
	} else if datagrams != nil {
		dialer, err = NewDialerWithUDPFast(mux, maxUDPPayload, datagrams)
	} else {
		dialer, err = NewDialerWithUDP(mux, maxUDPPayload)
	}
	if err != nil {
		return nil, err
	}
	return startWithDialer(fileDescriptor, mtu, dialer)
}

func startWithDialer(fileDescriptor int, mtu uint32, dialer *Dialer) (*Stack, error) {
	if fileDescriptor < 0 || mtu < minimumMTU || mtu > maximumMTU || dialer == nil {
		return nil, ErrInvalidStackConfig
	}
	var endpoint device.Device
	var err error
	if runtime.GOOS == "ios" {
		endpoint, err = newDarwinUTUNDevice(fileDescriptor, mtu)
	} else {
		endpoint, err = fdbased.Open(strconv.Itoa(fileDescriptor), mtu, 0)
	}
	if err != nil {
		return nil, err
	}
	return startWithDevice(endpoint, dialer)
}

func startWithDevice(endpoint device.Device, dialer *Dialer) (*Stack, error) {
	if endpoint == nil || dialer == nil {
		return nil, ErrInvalidStackConfig
	}
	statistic.DefaultManager.ResetStatistic()
	flowTunnel := tunnel.New(dialer, statistic.DefaultManager)
	flowTunnel.ProcessAsync()
	userspaceStack, err := core.CreateStack(&core.Config{
		LinkEndpoint: endpoint, TransportHandler: flowTunnel,
	})
	if err != nil {
		flowTunnel.Close()
		endpoint.Close()
		return nil, err
	}
	installUDPTransportHandler(userspaceStack, flowTunnel, dialer)
	return &Stack{device: endpoint, stack: userspaceStack, tunnel: flowTunnel, dialer: dialer}, nil
}

// TrafficStats returns current one-second throughput and cumulative payload
// bytes observed by the userspace TCP tunnel. Values exclude NP/2 cover and
// carrier overhead so the UI reports application traffic.
func (s *Stack) TrafficStats() (uploadRate, downloadRate, uploadTotal, downloadTotal int64) {
	if s == nil {
		return 0, 0, 0, 0
	}
	uploadRate, downloadRate = statistic.DefaultManager.Now()
	snapshot := statistic.DefaultManager.Snapshot()
	return uploadRate, downloadRate, snapshot.UploadTotal, snapshot.DownloadTotal
}

// DNSAttributionStats reports bounded aggregate counters without exposing
// queried names, target addresses or payloads.
func (s *Stack) DNSAttributionStats() DNSAttributionStats {
	if s == nil || s.dialer == nil {
		return DNSAttributionStats{}
	}
	stats := s.dialer.dns.stats()
	stats.FirstFlightDomainHits = s.dialer.firstFlightDomainHits.Load()
	stats.FirstFlightFallbacks = s.dialer.firstFlightFallbacks.Load()
	return stats
}

func (s *Stack) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.tunnel.Close()
		s.device.Close()
		s.stack.Close()
		s.stack.Wait()
	})
	return nil
}
