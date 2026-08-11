//go:build windows

package windowsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

const PipePath = `\\.\pipe\NeProto.Service.v1`

type PipeServer struct {
	api      *API
	listener net.Listener
	clients  chan struct{}
	wait     sync.WaitGroup
}

func NewPipeServer(api *API) (*PipeServer, error) {
	if api == nil {
		return nil, ErrInvalidIPCMessage
	}
	listener, err := winio.ListenPipe(PipePath, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;AU)",
		MessageMode:        false, InputBufferSize: MaxIPCMessageBytes + 4, OutputBufferSize: MaxIPCMessageBytes + 4,
	})
	if err != nil {
		return nil, err
	}
	return &PipeServer{api: api, listener: listener, clients: make(chan struct{}, 16)}, nil
}

func CallPipe(ctx context.Context, request Request) (Response, error) {
	connection, err := winio.DialPipeContext(ctx, PipePath)
	if err != nil {
		return Response{}, err
	}
	defer connection.Close()
	raw, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	if err := WriteFrame(connection, raw); err != nil {
		return Response{}, err
	}
	responseRaw, err := ReadFrame(connection)
	if err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.Unmarshal(responseRaw, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

func (s *PipeServer) Serve(ctx context.Context) error {
	if s == nil || s.listener == nil {
		return ErrInvalidIPCMessage
	}
	go func() { <-ctx.Done(); _ = s.listener.Close() }()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.wait.Wait()
				return nil
			}
			return err
		}
		select {
		case s.clients <- struct{}{}:
			s.wait.Add(1)
			go s.handle(connection)
		default:
			_ = connection.Close()
		}
	}
}

func (s *PipeServer) handle(connection net.Conn) {
	defer func() { <-s.clients; s.wait.Done(); _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	raw, err := ReadFrame(connection)
	response := Response{ID: "invalid", OK: false, Error: safeError(err)}
	if err == nil {
		request, decodeErr := DecodeRequest(bytes.NewReader(raw))
		if decodeErr != nil {
			response = Response{ID: "invalid", OK: false, Error: safeError(decodeErr)}
		} else {
			response = s.api.Handle(request)
		}
	}
	encoded, encodeErr := EncodeResponse(response)
	if encodeErr != nil {
		return
	}
	_ = WriteFrame(connection, encoded)
}
