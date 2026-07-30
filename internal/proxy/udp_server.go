package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

const (
	maxUDPAssociationTargets = 1024
	antiAmplificationMTU     = 1280
)

type PacketListener interface {
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
}

func (s Server) handleUDPIncoming(
	ctx context.Context,
	incoming *session.Incoming,
	request OpenRequest,
) error {
	if !s.acquireUDPAssociation() {
		if s.UDPStats != nil {
			s.UDPStats.associationLimitRejects.Add(1)
		}
		_ = incoming.Reject(socks5.ReplyGeneralFailure)
		return ErrUDPAssociationLimit
	}
	defer s.releaseUDPAssociation()
	if s.ResourceLimiter != nil {
		if !s.ResourceLimiter.AcquireUDP(s.CredentialID) {
			if s.UDPStats != nil {
				s.UDPStats.associationLimitRejects.Add(1)
			}
			_ = incoming.Reject(socks5.ReplyGeneralFailure)
			return ErrUDPAssociationLimit
		}
		defer s.ResourceLimiter.ReleaseUDP(s.CredentialID)
	}
	maximumPayload, allowed := s.udpPayloadLimit(ctx)
	if !allowed {
		_ = incoming.Reject(socks5.ReplyCommandNotSupported)
		return ErrInvalidConfig
	}
	if request.ClientRoute != nil && s.RouteClientUDP == nil {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	routed := s.RouteUDP != nil || request.ClientRoute != nil
	var fixedAddress netip.AddrPort
	if request.Command == CommandUDPFixed && !routed {
		address, err := s.resolveUDPAddress(ctx, request.Target)
		if err != nil {
			code := byte(socks5.ReplyHostUnreachable)
			if errors.Is(err, ErrDestinationDenied) {
				code = socks5.ReplyNotAllowed
				if s.UDPStats != nil {
					s.UDPStats.policyDrops.Add(1)
				}
			}
			_ = incoming.Reject(code)
			return err
		}
		fixedAddress = address
	}
	var packetConnection net.PacketConn
	var err error
	if !routed {
		listener := s.PacketListener
		if listener == nil {
			listener = &net.ListenConfig{}
		}
		packetConnection, err = listener.ListenPacket(ctx, "udp", ":0")
		if err != nil {
			_ = incoming.Reject(socks5.ReplyGeneralFailure)
			return err
		}
	}
	var stream *session.Stream
	var endpoint *session.DatagramEndpoint
	if s.Datagrams != nil && s.Datagrams.Enabled() {
		stream, endpoint, err = incoming.AcceptWithDatagrams(s.Datagrams)
	} else {
		stream, err = incoming.Accept()
	}
	if err != nil {
		if packetConnection != nil {
			_ = packetConnection.Close()
		}
		return err
	}
	var association *UDPAssociation
	if endpoint != nil {
		association, err = NewUDPAssociationWithDatagrams(
			stream, request.Command, request.Target, maximumPayload, endpoint,
		)
	} else {
		association, err = NewUDPAssociation(stream, request.Command, request.Target, maximumPayload)
	}
	if err != nil {
		if packetConnection != nil {
			_ = packetConnection.Close()
		}
		_ = stream.Close()
		return err
	}
	s.UDPStats.associationOpened()
	defer s.UDPStats.associationClosed()
	idleTimeout := s.UDPIdleTimeout
	if idleTimeout == 0 {
		idleTimeout = defaultUDPIdleTimeout
	}
	if routed {
		return s.relayRoutedUDPAssociation(
			ctx, association, request.Command, maximumPayload, idleTimeout,
			request.ClientRoute,
		)
	}
	return s.relayUDPAssociation(
		ctx, association, packetConnection, request.Command, fixedAddress,
		maximumPayload, idleTimeout,
	)
}

func (s Server) relayRoutedUDPAssociation(
	ctx context.Context,
	association *UDPAssociation,
	command OpenCommand,
	maxPayload uint64,
	idleTimeout time.Duration,
	clientRoute *cluster.ClientRouteRequest,
) error {
	type routedTarget struct {
		stream      DuplexStream
		association *UDPAssociation
	}
	routes := make(map[Target]routedTarget)
	var routesMu sync.Mutex
	associationContext, cancel := context.WithCancel(ctx)
	activity := make(chan struct{}, 1)
	results := make(chan error, 1)
	var workers sync.WaitGroup
	report := func(err error) {
		select {
		case results <- err:
		default:
		}
	}
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			payload, target, err := association.ReadDatagram()
			if err != nil {
				if errors.Is(err, io.EOF) {
					report(nil)
				} else {
					report(err)
				}
				return
			}
			logicalTarget := target
			if command == CommandUDPFixed {
				logicalTarget = association.fixed
			}
			routesMu.Lock()
			route, exists := routes[logicalTarget]
			routeCount := len(routes)
			routesMu.Unlock()
			newTarget := !exists
			if s.ResourceLimiter != nil && !s.ResourceLimiter.AllowUDPPacket(
				s.CredentialID, len(payload), logicalTarget.Port == 53, newTarget,
			) {
				if s.UDPStats != nil {
					s.UDPStats.rateLimitDrops.Add(1)
				}
				_ = association.WriteError(UDPErrorRateLimited, "rate limit reached")
				continue
			}
			if !exists {
				if routeCount >= maxUDPAssociationTargets {
					if s.UDPStats != nil {
						s.UDPStats.targetLimitDrops.Add(1)
					}
					_ = association.WriteError(UDPErrorRateLimited, "target limit reached")
					continue
				}
				var stream DuplexStream
				var handled bool
				var routeErr error
				if clientRoute != nil {
					stream, handled, routeErr = s.RouteClientUDP(
						associationContext, s.CredentialID, logicalTarget, *clientRoute,
					)
				} else {
					stream, handled, routeErr = s.RouteUDP(associationContext, s.CredentialID, logicalTarget)
				}
				if routeErr != nil {
					if errors.Is(routeErr, ErrDestinationDenied) {
						_ = association.WriteError(UDPErrorPolicyDenied, "destination denied")
					} else {
						_ = association.WriteError(UDPErrorResolution, "route unavailable")
					}
					continue
				}
				if !handled {
					stream, routeErr = newUDPTerminalStream(
						associationContext, logicalTarget, maxPayload, idleTimeout,
						s.Policy, s.Resolver, s.PacketListener, s.UDPStats,
					)
				}
				if routeErr != nil || stream == nil {
					_ = association.WriteError(UDPErrorResolution, "route unavailable")
					continue
				}
				remote, routeErr := NewUDPAssociation(stream, CommandUDPFixed, logicalTarget, maxPayload)
				if routeErr != nil {
					_ = stream.Close()
					_ = association.WriteError(UDPErrorResolution, "route unavailable")
					continue
				}
				route = routedTarget{stream: stream, association: remote}
				routesMu.Lock()
				routes[logicalTarget] = route
				routesMu.Unlock()
				workers.Add(1)
				go func(target Target, remote *UDPAssociation) {
					defer workers.Done()
					for {
						response, _, readErr := remote.ReadDatagram()
						if readErr != nil {
							return
						}
						var responseTarget *Target
						if command == CommandUDPAssociate {
							copyTarget := target
							responseTarget = &copyTarget
						}
						if writeErr := association.WriteDatagram(response, responseTarget); writeErr != nil {
							report(writeErr)
							return
						}
						if s.UDPStats != nil {
							s.UDPStats.targetDatagrams.Add(1)
							s.UDPStats.targetBytes.Add(uint64(len(response)))
						}
						touch()
					}
				}(logicalTarget, remote)
			}
			if err := route.association.WriteDatagram(payload, nil); err != nil {
				report(err)
				return
			}
			if s.UDPStats != nil {
				s.UDPStats.clientDatagrams.Add(1)
				s.UDPStats.clientBytes.Add(uint64(len(payload)))
			}
			touch()
		}
	}()

	cleanup := func() {
		cancel()
		_ = association.Abort()
		routesMu.Lock()
		for _, route := range routes {
			_ = route.association.Abort()
			_ = route.stream.Close()
		}
		routesMu.Unlock()
		workers.Wait()
	}
	defer cleanup()

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	for {
		select {
		case err := <-results:
			return err
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)
		case <-timer.C:
			if s.UDPStats != nil {
				s.UDPStats.idleExpirations.Add(1)
			}
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func (s Server) acquireUDPAssociation() bool {
	if s.udpSemaphore == nil {
		return true
	}
	select {
	case s.udpSemaphore <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s Server) releaseUDPAssociation() {
	if s.udpSemaphore != nil {
		<-s.udpSemaphore
	}
}

func (s Server) udpPayloadLimit(ctx context.Context) (uint64, bool) {
	maximum := s.MaxUDPPayload
	if s.AuthorizeUDP != nil {
		authorizedMaximum, ok := s.AuthorizeUDP(ctx)
		if !ok || authorizedMaximum < 1200 || authorizedMaximum > MaxUDPDatagramPayload {
			return 0, false
		}
		if maximum == 0 || authorizedMaximum < maximum {
			maximum = authorizedMaximum
		}
	}
	if maximum < 1200 || maximum > MaxUDPDatagramPayload {
		return 0, false
	}
	return maximum, true
}

func (s Server) relayUDPAssociation(
	ctx context.Context,
	association *UDPAssociation,
	packetConnection net.PacketConn,
	command OpenCommand,
	fixedAddress netip.AddrPort,
	maxPayload uint64,
	idleTimeout time.Duration,
) error {
	associationContext, cancel := context.WithCancel(ctx)
	defer cancel()
	defer packetConnection.Close()
	defer association.Abort()
	allowed := newAllowedUDPAddresses()
	seenTargets := make(map[Target]struct{})
	refreshDeadline := func() {
		_ = packetConnection.SetReadDeadline(time.Now().Add(idleTimeout))
	}
	refreshDeadline()
	results := make(chan error, 2)
	go func() {
		for {
			payload, target, err := association.ReadDatagram()
			if err != nil {
				if errors.Is(err, io.EOF) {
					results <- nil
				} else {
					results <- err
				}
				return
			}
			logicalTarget := target
			if command == CommandUDPFixed {
				logicalTarget = association.fixed
			}
			_, targetSeen := seenTargets[logicalTarget]
			newTarget := !targetSeen
			if newTarget && len(seenTargets) >= maxUDPAssociationTargets {
				if s.UDPStats != nil {
					s.UDPStats.targetLimitDrops.Add(1)
				}
				_ = association.WriteError(UDPErrorRateLimited, "target limit reached")
				continue
			}
			if s.ResourceLimiter != nil &&
				!s.ResourceLimiter.AllowUDPPacket(s.CredentialID, len(payload), logicalTarget.Port == 53, newTarget) {
				if s.UDPStats != nil {
					s.UDPStats.rateLimitDrops.Add(1)
				}
				_ = association.WriteError(UDPErrorRateLimited, "rate limit reached")
				continue
			}
			if newTarget {
				seenTargets[logicalTarget] = struct{}{}
			}
			if s.UDPStats != nil {
				s.UDPStats.clientDatagrams.Add(1)
				s.UDPStats.clientBytes.Add(uint64(len(payload)))
			}
			address := fixedAddress
			if command == CommandUDPAssociate {
				address, err = s.resolveUDPAddress(associationContext, target)
				if err != nil {
					code := UDPErrorResolution
					if errors.Is(err, ErrDestinationDenied) {
						code = UDPErrorPolicyDenied
						if s.UDPStats != nil {
							s.UDPStats.policyDrops.Add(1)
						}
					}
					_ = association.WriteError(code, "destination unavailable")
					continue
				}
				if ok, _ := allowed.Add(address); !ok {
					if s.UDPStats != nil {
						s.UDPStats.targetLimitDrops.Add(1)
					}
					_ = association.WriteError(UDPErrorRateLimited, "target limit reached")
					continue
				}
			}
			if command == CommandUDPFixed {
				if ok, _ := allowed.Add(address); !ok {
					_ = association.WriteError(UDPErrorRateLimited, "target limit reached")
					continue
				}
			}
			allowed.AddClientBytes(address, len(payload))
			refreshDeadline()
			if _, err := packetConnection.WriteTo(payload, net.UDPAddrFromAddrPort(address)); err != nil {
				if s.UDPStats != nil {
					s.UDPStats.relayErrors.Add(1)
				}
				results <- err
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, int(maxPayload)+1)
		for {
			length, source, err := packetConnection.ReadFrom(buffer)
			if err != nil {
				var networkError net.Error
				if errors.As(err, &networkError) && networkError.Timeout() {
					if s.UDPStats != nil {
						s.UDPStats.idleExpirations.Add(1)
					}
					results <- nil
				} else if associationContext.Err() != nil || errors.Is(err, net.ErrClosed) {
					results <- nil
				} else {
					if s.UDPStats != nil {
						s.UDPStats.relayErrors.Add(1)
					}
					results <- err
				}
				return
			}
			refreshDeadline()
			if uint64(length) > maxPayload {
				if s.UDPStats != nil {
					s.UDPStats.oversizedDrops.Add(1)
				}
				continue
			}
			sourceAddress, ok := udpAddrPort(source)
			if !ok || !allowed.Contains(sourceAddress) {
				if s.UDPStats != nil {
					s.UDPStats.unexpectedSourceDrops.Add(1)
				}
				continue
			}
			if !allowed.AllowReply(sourceAddress, length, antiAmplificationMTU) {
				if s.UDPStats != nil {
					s.UDPStats.amplificationDrops.Add(1)
				}
				continue
			}
			if s.UDPStats != nil {
				s.UDPStats.targetDatagrams.Add(1)
				s.UDPStats.targetBytes.Add(uint64(length))
			}
			var target *Target
			if command == CommandUDPAssociate {
				numericTarget := Target{Host: sourceAddress.Addr().String(), Port: sourceAddress.Port()}
				target = &numericTarget
			}
			if err := association.WriteDatagram(buffer[:length], target); err != nil {
				results <- err
				return
			}
		}
	}()

	first := <-results
	cancel()
	_ = packetConnection.Close()
	_ = association.Abort()
	second := <-results
	if first != nil {
		return first
	}
	return second
}

func (s Server) resolveUDPAddress(ctx context.Context, target Target) (netip.AddrPort, error) {
	resolver := s.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := s.Policy.Resolve(ctx, target, resolver)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if len(addresses) == 0 {
		return netip.AddrPort{}, ErrResolution
	}
	return netip.AddrPortFrom(addresses[0], target.Port), nil
}

func udpAddrPort(address net.Addr) (netip.AddrPort, bool) {
	udpAddress, ok := address.(*net.UDPAddr)
	if !ok {
		return netip.AddrPort{}, false
	}
	result := udpAddress.AddrPort()
	if !result.IsValid() {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(result.Addr().Unmap(), result.Port()), true
}

type allowedUDPAddresses struct {
	mu        sync.RWMutex
	addresses map[netip.AddrPort]*udpTargetAccounting
}

type udpTargetAccounting struct {
	clientBytes uint64
	replied     bool
}

func newAllowedUDPAddresses() *allowedUDPAddresses {
	return &allowedUDPAddresses{addresses: make(map[netip.AddrPort]*udpTargetAccounting)}
}

func (a *allowedUDPAddresses) Add(address netip.AddrPort) (bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.addresses[address]; exists {
		return true, false
	}
	if len(a.addresses) >= maxUDPAssociationTargets {
		return false, false
	}
	a.addresses[address] = &udpTargetAccounting{}
	return true, true
}

func (a *allowedUDPAddresses) Contains(address netip.AddrPort) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, exists := a.addresses[address]
	return exists
}

func (a *allowedUDPAddresses) AddClientBytes(address netip.AddrPort, count int) {
	if count <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.addresses[address]
	if state == nil {
		return
	}
	value := uint64(count)
	if ^uint64(0)-state.clientBytes < value {
		state.clientBytes = ^uint64(0)
	} else {
		state.clientBytes += value
	}
}

func (a *allowedUDPAddresses) AllowReply(address netip.AddrPort, count, mtu int) bool {
	if count < 0 || mtu < 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.addresses[address]
	if state == nil {
		return false
	}
	if state.replied {
		return true
	}
	allowance := uint64(mtu)
	if state.clientBytes > (^uint64(0)-allowance)/3 {
		allowance = ^uint64(0)
	} else {
		allowance += 3 * state.clientBytes
	}
	if uint64(count) > allowance {
		return false
	}
	state.replied = true
	return true
}
