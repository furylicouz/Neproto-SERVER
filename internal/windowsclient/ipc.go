package windowsclient

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxIPCMessageBytes = 256 << 10

type Method string

const (
	MethodStatus           Method = "status"
	MethodProfilesList     Method = "profiles.list"
	MethodProfilesImport   Method = "profiles.import"
	MethodProfilesRemove   Method = "profiles.remove"
	MethodProfilesSelect   Method = "profiles.select"
	MethodTunnelConnect    Method = "tunnel.connect"
	MethodTunnelDisconnect Method = "tunnel.disconnect"
	MethodCatalogSync      Method = "catalog.sync"
	MethodRoutesList       Method = "routes.list"
	MethodRoutesUpsert     Method = "routes.upsert"
	MethodRoutesRemove     Method = "routes.remove"
	MethodLogsTail         Method = "logs.tail"
)

var ErrInvalidIPCMessage = errors.New("invalid NeProto IPC message")

type Request struct {
	ID     string          `json:"id"`
	Method Method          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func DecodeRequest(reader io.Reader) (Request, error) {
	if reader == nil {
		return Request{}, ErrInvalidIPCMessage
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxIPCMessageBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > MaxIPCMessageBytes || !utf8.Valid(raw) {
		return Request{}, ErrInvalidIPCMessage
	}
	if err := rejectDuplicateTopLevelFields(raw); err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, ErrInvalidIPCMessage
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Request{}, ErrInvalidIPCMessage
	}
	if !validRequestID(request.ID) || !validMethod(request.Method) || len(request.Params) == 0 || request.Params[0] != '{' {
		return Request{}, ErrInvalidIPCMessage
	}
	return request, nil
}

func EncodeResponse(response Response) ([]byte, error) {
	if !validRequestID(response.ID) || response.OK == (response.Error != "") || len(response.Error) > 512 {
		return nil, ErrInvalidIPCMessage
	}
	raw, err := json.Marshal(response)
	if err != nil || len(raw) > MaxIPCMessageBytes {
		return nil, ErrInvalidIPCMessage
	}
	return append(raw, '\n'), nil
}

func ReadFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, ErrInvalidIPCMessage
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > MaxIPCMessageBytes {
		return nil, ErrInvalidIPCMessage
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, ErrInvalidIPCMessage
	}
	return payload, nil
}

func WriteFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxIPCMessageBytes {
		return ErrInvalidIPCMessage
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func validRequestID(value string) bool {
	if len(value) < 1 || len(value) > 64 || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validMethod(method Method) bool {
	switch method {
	case MethodStatus, MethodProfilesList, MethodProfilesImport, MethodProfilesRemove,
		MethodProfilesSelect, MethodTunnelConnect, MethodTunnelDisconnect,
		MethodCatalogSync, MethodRoutesList, MethodRoutesUpsert, MethodRoutesRemove, MethodLogsTail:
		return true
	default:
		return false
	}
}

func rejectDuplicateTopLevelFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidIPCMessage
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return ErrInvalidIPCMessage
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate field", ErrInvalidIPCMessage)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ErrInvalidIPCMessage
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ErrInvalidIPCMessage
	}
	return nil
}
