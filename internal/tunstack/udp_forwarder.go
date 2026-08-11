package tunstack

import (
	glog "gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
)

// installUDPTransportHandler replaces tun2socks' unconditional UDP forwarder
// during stack initialization. Reliable-only NP/2 routes reject UDP/443 before
// an endpoint is created, allowing gVisor to return ICMP port-unreachable and
// applications to retry immediately over HTTPS/TCP. Every other UDP flow keeps
// the normal tun2socks path.
func installUDPTransportHandler(userspaceStack *stack.Stack, flowTunnel *tunnel.Tunnel, dialer *Dialer) {
	udpForwarder := udp.NewForwarder(userspaceStack, func(request *udp.ForwarderRequest) {
		var queue waiter.Queue
		id := request.ID()
		endpoint, err := request.CreateEndpoint(&queue)
		if err != nil {
			glog.Debugf(
				"forward udp request: %s:%d->%s:%d: %s",
				id.RemoteAddress,
				id.RemotePort,
				id.LocalAddress,
				id.LocalPort,
				err,
			)
			return
		}
		flowTunnel.HandleUDP(&forwardedUDPConn{
			UDPConn: gonet.NewUDPConn(&queue, endpoint),
			id:      id,
		})
	})

	var reject func(uint16) bool
	if dialer != nil {
		reject = dialer.rejectUDP
	}
	userspaceStack.SetTransportProtocolHandler(
		udp.ProtocolNumber,
		newUDPTransportHandler(udpForwarder.HandlePacket, reject),
	)
}

func newUDPTransportHandler(
	forward func(stack.TransportEndpointID, *stack.PacketBuffer) bool,
	reject func(uint16) bool,
) func(stack.TransportEndpointID, *stack.PacketBuffer) bool {
	return func(id stack.TransportEndpointID, packet *stack.PacketBuffer) bool {
		if reject != nil && reject(id.LocalPort) {
			return false
		}
		return forward(id, packet)
	}
}

type forwardedUDPConn struct {
	*gonet.UDPConn
	id stack.TransportEndpointID
}

var _ adapter.UDPConn = (*forwardedUDPConn)(nil)

func (connection *forwardedUDPConn) ID() *stack.TransportEndpointID {
	return &connection.id
}
