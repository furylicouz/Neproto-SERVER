package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	version5            byte = 5
	methodNoAuth        byte = 0
	methodUnacceptable  byte = 0xff
	commandConnect      byte = 1
	commandUDPAssociate byte = 3
	addressIPv4         byte = 1
	addressDomain       byte = 3
	addressIPv6         byte = 4

	ReplySucceeded           byte = 0
	ReplyGeneralFailure      byte = 1
	ReplyNotAllowed          byte = 2
	ReplyNetworkUnreachable  byte = 3
	ReplyHostUnreachable     byte = 4
	ReplyConnectionRefused   byte = 5
	ReplyTTLExpired          byte = 6
	ReplyCommandNotSupported byte = 7
	ReplyAddressNotSupported byte = 8

	defaultMaxConnections = 128
	defaultHandshakeTime  = 10 * time.Second
)

var (
	ErrUnsafeBind    = errors.New("SOCKS listener must use a loopback address")
	ErrInvalidConfig = errors.New("invalid SOCKS server configuration")
	ErrProtocol      = errors.New("invalid SOCKS5 request")
)

type Request struct {
	Host string
	Port uint16
}

type ReplyError struct {
	Code byte
}

func (e *ReplyError) Error() string {
	return fmt.Sprintf("SOCKS request failed (reply %d)", e.Code)
}

type ConnectFunc func(context.Context, Request) (io.ReadWriteCloser, error)

type UDPAssociation interface {
	WriteDatagram([]byte, Request) error
	ReadDatagram() ([]byte, Request, error)
	Close() error
}

type AssociateUDPFunc func(context.Context) (UDPAssociation, error)

type Server struct {
	Connect          ConnectFunc
	AssociateUDP     AssociateUDPFunc
	MaxConnections   int
	HandshakeTimeout time.Duration
}

func (s Server) Serve(ctx context.Context, listener net.Listener) error {
	if ctx == nil || listener == nil || s.Connect == nil || s.MaxConnections < 0 || s.HandshakeTimeout < 0 {
		return ErrInvalidConfig
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return ErrUnsafeBind
	}
	maximum := s.MaxConnections
	if maximum == 0 {
		maximum = defaultMaxConnections
	}
	serveContext, cancelServe := context.WithCancel(ctx)
	stopListener := context.AfterFunc(serveContext, func() { _ = listener.Close() })
	defer stopListener()

	semaphore := make(chan struct{}, maximum)
	var wait sync.WaitGroup
	defer func() {
		cancelServe()
		wait.Wait()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if serveContext.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			wait.Add(1)
			go func() {
				defer wait.Done()
				defer func() { <-semaphore }()
				_ = s.serveConn(serveContext, connection)
			}()
		case <-serveContext.Done():
			_ = connection.Close()
			return nil
		}
	}
}

func (s Server) serveConn(ctx context.Context, connection net.Conn) error {
	if s.Connect == nil {
		_ = connection.Close()
		return ErrInvalidConfig
	}
	defer connection.Close()
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopConnection()
	timeout := s.HandshakeTimeout
	if timeout == 0 {
		timeout = defaultHandshakeTime
	}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	accepted, err := negotiateMethod(connection)
	if err != nil || !accepted {
		return err
	}
	request, reply, err := readRequest(connection)
	if err != nil {
		if reply != 0 {
			if writeErr := writeReply(connection, reply); writeErr != nil {
				return writeErr
			}
			return nil
		}
		return err
	}
	switch request.command {
	case commandConnect:
		connectContext, cancelConnect := context.WithTimeout(ctx, timeout)
		upstream, connectErr := s.Connect(connectContext, request.target)
		cancelConnect()
		if connectErr != nil {
			_ = writeReply(connection, replyCode(connectErr))
			return nil
		}
		defer upstream.Close()
		stopUpstream := context.AfterFunc(ctx, func() { _ = upstream.Close() })
		defer stopUpstream()
		if err := writeReply(connection, ReplySucceeded); err != nil {
			return err
		}
		if err := connection.SetDeadline(time.Time{}); err != nil {
			return err
		}
		return relay(ctx, connection, upstream)
	case commandUDPAssociate:
		if s.AssociateUDP == nil {
			_ = writeReply(connection, ReplyCommandNotSupported)
			return nil
		}
		return s.serveUDPAssociate(ctx, connection, request.target, timeout)
	default:
		_ = writeReply(connection, ReplyCommandNotSupported)
		return nil
	}
}

func negotiateMethod(connection io.ReadWriter) (bool, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil {
		return false, err
	}
	if header[0] != version5 || header[1] == 0 {
		return false, ErrProtocol
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return false, err
	}
	selected := methodUnacceptable
	for _, method := range methods {
		if method == methodNoAuth {
			selected = methodNoAuth
			break
		}
	}
	if _, err := connection.Write([]byte{version5, selected}); err != nil {
		return false, err
	}
	return selected != methodUnacceptable, nil
}

type clientRequest struct {
	command byte
	target  Request
}

func readRequest(connection io.Reader) (clientRequest, byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil {
		return clientRequest{}, ReplyGeneralFailure, err
	}
	if header[0] != version5 || header[2] != 0 {
		return clientRequest{}, ReplyGeneralFailure, ErrProtocol
	}
	var host string
	switch header[3] {
	case addressIPv4:
		raw := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(connection, raw); err != nil {
			return clientRequest{}, ReplyAddressNotSupported, err
		}
		host = net.IP(raw).String()
	case addressDomain:
		length := []byte{0}
		if _, err := io.ReadFull(connection, length); err != nil || length[0] == 0 {
			return clientRequest{}, ReplyAddressNotSupported, ErrProtocol
		}
		raw := make([]byte, int(length[0]))
		if _, err := io.ReadFull(connection, raw); err != nil {
			return clientRequest{}, ReplyAddressNotSupported, err
		}
		host = string(raw)
	case addressIPv6:
		raw := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(connection, raw); err != nil {
			return clientRequest{}, ReplyAddressNotSupported, err
		}
		host = net.IP(raw).String()
	default:
		return clientRequest{}, ReplyAddressNotSupported, ErrProtocol
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(connection, portBytes); err != nil {
		return clientRequest{}, ReplyGeneralFailure, err
	}
	port := binary.BigEndian.Uint16(portBytes)
	if header[1] != commandConnect && header[1] != commandUDPAssociate {
		return clientRequest{}, ReplyCommandNotSupported, ErrProtocol
	}
	if header[1] == commandConnect && port == 0 {
		return clientRequest{}, ReplyGeneralFailure, ErrProtocol
	}
	return clientRequest{command: header[1], target: Request{Host: host, Port: port}}, 0, nil
}

func writeReply(writer io.Writer, code byte) error {
	_, err := writer.Write([]byte{version5, code, 0, addressIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func replyCode(err error) byte {
	var reply *ReplyError
	if errors.As(err, &reply) && reply.Code >= ReplyGeneralFailure && reply.Code <= ReplyAddressNotSupported {
		return reply.Code
	}
	return ReplyGeneralFailure
}
