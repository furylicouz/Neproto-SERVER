package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

const maxSOCKSUDPDatagram = 65_507

func (s Server) serveUDPAssociate(
	ctx context.Context,
	control net.Conn,
	hint Request,
	timeout time.Duration,
) error {
	tracker, err := newUDPClientTracker(control, hint)
	if err != nil {
		_ = writeReply(control, ReplyNotAllowed)
		return nil
	}
	associateContext, cancelAssociate := context.WithTimeout(ctx, timeout)
	association, err := s.AssociateUDP(associateContext)
	cancelAssociate()
	if err != nil {
		_ = writeReply(control, replyCode(err))
		return nil
	}
	defer association.Close()

	udpNetwork := "udp4"
	udpAddress := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	if local, ok := control.LocalAddr().(*net.TCPAddr); ok && local.IP.To4() == nil {
		udpNetwork = "udp6"
		udpAddress.IP = net.IPv6loopback
	}
	relay, err := net.ListenUDP(udpNetwork, udpAddress)
	if err != nil {
		_ = writeReply(control, ReplyGeneralFailure)
		return nil
	}
	defer relay.Close()
	if err := writeReplyAddress(control, ReplySucceeded, relay.LocalAddr().(*net.UDPAddr)); err != nil {
		return err
	}
	if err := control.SetDeadline(time.Time{}); err != nil {
		return err
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 3)
	go readUDPControl(control, results)
	go relaySOCKSUDPToAssociation(runContext, relay, tracker, association, results)
	go relayAssociationToSOCKSUDP(runContext, relay, tracker, association, results)

	var result error
	select {
	case result = <-results:
	case <-runContext.Done():
		result = runContext.Err()
	}
	cancel()
	_ = relay.Close()
	_ = association.Close()
	_ = control.Close()
	for count := 0; count < 2; count++ {
		<-results
	}
	if result == nil || errors.Is(result, io.EOF) || errors.Is(result, net.ErrClosed) ||
		errors.Is(result, context.Canceled) {
		return nil
	}
	return result
}

func readUDPControl(control net.Conn, results chan<- error) {
	var one [1]byte
	_, err := control.Read(one[:])
	results <- err
}

func relaySOCKSUDPToAssociation(
	ctx context.Context,
	relay *net.UDPConn,
	tracker *udpClientTracker,
	association UDPAssociation,
	results chan<- error,
) {
	buffer := make([]byte, maxSOCKSUDPDatagram)
	for {
		length, source, err := relay.ReadFromUDP(buffer)
		if err != nil {
			results <- err
			return
		}
		if !tracker.Accept(source) {
			continue
		}
		target, payload, err := decodeSOCKSUDPDatagram(buffer[:length])
		if err != nil {
			continue
		}
		if err := association.WriteDatagram(payload, target); err != nil {
			results <- err
			return
		}
		select {
		case <-ctx.Done():
			results <- ctx.Err()
			return
		default:
		}
	}
}

func relayAssociationToSOCKSUDP(
	ctx context.Context,
	relay *net.UDPConn,
	tracker *udpClientTracker,
	association UDPAssociation,
	results chan<- error,
) {
	for {
		payload, target, err := association.ReadDatagram()
		if err != nil {
			results <- err
			return
		}
		raw, err := encodeUDPDatagram(target, payload)
		if err != nil {
			continue
		}
		client := tracker.Client()
		if client == nil {
			continue
		}
		if _, err := relay.WriteToUDP(raw, client); err != nil {
			results <- err
			return
		}
		select {
		case <-ctx.Done():
			results <- ctx.Err()
			return
		default:
		}
	}
}

func decodeSOCKSUDPDatagram(raw []byte) (Request, []byte, error) {
	if len(raw) < 7 || raw[0] != 0 || raw[1] != 0 || raw[2] != 0 {
		return Request{}, nil, ErrProtocol
	}
	host, cursor, err := decodeUDPHost(raw, 3)
	if err != nil || len(raw)-cursor < 2 {
		return Request{}, nil, ErrProtocol
	}
	port := binary.BigEndian.Uint16(raw[cursor : cursor+2])
	if port == 0 {
		return Request{}, nil, ErrProtocol
	}
	payload := append([]byte(nil), raw[cursor+2:]...)
	return Request{Host: host, Port: port}, payload, nil
}

func decodeUDPHost(raw []byte, cursor int) (string, int, error) {
	if cursor >= len(raw) {
		return "", cursor, ErrProtocol
	}
	switch raw[cursor] {
	case addressIPv4:
		cursor++
		if len(raw)-cursor < net.IPv4len {
			return "", cursor, ErrProtocol
		}
		return net.IP(raw[cursor : cursor+net.IPv4len]).String(), cursor + net.IPv4len, nil
	case addressIPv6:
		cursor++
		if len(raw)-cursor < net.IPv6len {
			return "", cursor, ErrProtocol
		}
		return net.IP(raw[cursor : cursor+net.IPv6len]).String(), cursor + net.IPv6len, nil
	case addressDomain:
		cursor++
		if cursor >= len(raw) || raw[cursor] == 0 || len(raw)-cursor-1 < int(raw[cursor]) {
			return "", cursor, ErrProtocol
		}
		length := int(raw[cursor])
		cursor++
		return string(raw[cursor : cursor+length]), cursor + length, nil
	default:
		return "", cursor, ErrProtocol
	}
}

func encodeUDPDatagram(target Request, payload []byte) ([]byte, error) {
	if target.Port == 0 {
		return nil, ErrProtocol
	}
	raw := []byte{0, 0, 0}
	address, err := netip.ParseAddr(target.Host)
	if err == nil {
		address = address.Unmap()
		if address.Is4() {
			raw = append(raw, addressIPv4)
			raw = append(raw, address.AsSlice()...)
		} else {
			raw = append(raw, addressIPv6)
			raw = append(raw, address.AsSlice()...)
		}
	} else {
		if len(target.Host) == 0 || len(target.Host) > 255 {
			return nil, ErrProtocol
		}
		raw = append(raw, addressDomain, byte(len(target.Host)))
		raw = append(raw, target.Host...)
	}
	raw = binary.BigEndian.AppendUint16(raw, target.Port)
	if len(raw)+len(payload) > maxSOCKSUDPDatagram {
		return nil, ErrProtocol
	}
	raw = append(raw, payload...)
	return raw, nil
}

func writeReplyAddress(writer io.Writer, code byte, address *net.UDPAddr) error {
	if address == nil || address.Port <= 0 || address.Port > 65535 {
		return ErrProtocol
	}
	raw := []byte{version5, code, 0}
	if ipv4 := address.IP.To4(); ipv4 != nil {
		raw = append(raw, addressIPv4)
		raw = append(raw, ipv4...)
	} else if ipv6 := address.IP.To16(); ipv6 != nil {
		raw = append(raw, addressIPv6)
		raw = append(raw, ipv6...)
	} else {
		return ErrProtocol
	}
	raw = binary.BigEndian.AppendUint16(raw, uint16(address.Port))
	_, err := writer.Write(raw)
	return err
}

type udpClientTracker struct {
	mu       sync.Mutex
	peerIP   netip.Addr
	hintPort uint16
	client   netip.AddrPort
}

func newUDPClientTracker(control net.Conn, hint Request) (*udpClientTracker, error) {
	remote, ok := control.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, ErrProtocol
	}
	peerIP, ok := netip.AddrFromSlice(remote.IP)
	if !ok || !peerIP.Unmap().IsLoopback() {
		return nil, ErrProtocol
	}
	peerIP = peerIP.Unmap()
	if hint.Host != "" {
		if hinted, parseErr := netip.ParseAddr(hint.Host); parseErr != nil {
			return nil, ErrProtocol
		} else if !hinted.IsUnspecified() && hinted.Unmap() != peerIP {
			return nil, ErrProtocol
		}
	}
	return &udpClientTracker{peerIP: peerIP, hintPort: hint.Port}, nil
}

func (t *udpClientTracker) Accept(source *net.UDPAddr) bool {
	if t == nil || source == nil || source.Port <= 0 || source.Port > 65535 {
		return false
	}
	address, ok := netip.AddrFromSlice(source.IP)
	if !ok {
		return false
	}
	address = address.Unmap()
	if address != t.peerIP || !address.IsLoopback() || (t.hintPort != 0 && uint16(source.Port) != t.hintPort) {
		return false
	}
	candidate := netip.AddrPortFrom(address, uint16(source.Port))
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.client.IsValid() {
		t.client = candidate
	}
	return t.client == candidate
}

func (t *udpClientTracker) Client() *net.UDPAddr {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if !client.IsValid() {
		return nil
	}
	return net.UDPAddrFromAddrPort(client)
}
