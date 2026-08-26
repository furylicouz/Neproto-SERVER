package http3wt

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func BenchmarkSessionCoverDownload(b *testing.B) {
	_, clientCarrier, serverCarrier := newCarrierPair(b, 1)
	typeMap, err := protocol.NewTypeMap([32]byte{0xd1, 0x27})
	if err != nil {
		b.Fatal(err)
	}
	clientCover := newBenchmarkCover(b, clientCarrier, typeMap, 0x31, 0x41)
	serverCover := newBenchmarkCover(b, serverCarrier, typeMap, 0x32, 0x42)
	clientMux, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: clientCover, TypeMap: typeMap,
		InitialWindow: 2 * 1024 * 1024, MaxStreams: 128,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = clientMux.Close() })
	serverMux, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: serverCover, TypeMap: typeMap,
		InitialWindow: 2 * 1024 * 1024, MaxStreams: 128,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = serverMux.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accepted := make(chan *session.Stream, 1)
	acceptErr := make(chan error, 1)
	go func() {
		incoming, err := serverMux.Accept(ctx)
		if err != nil {
			acceptErr <- err
			return
		}
		stream, err := incoming.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- stream
	}()
	clientStream, err := clientMux.Open(ctx, []byte("benchmark"))
	if err != nil {
		b.Fatal(err)
	}
	var serverStream *session.Stream
	select {
	case serverStream = <-accepted:
	case err := <-acceptErr:
		b.Fatal(err)
	case <-ctx.Done():
		b.Fatal(ctx.Err())
	}

	payload := bytes.Repeat([]byte{0xa5}, 20*1024)
	received := make(chan error, 1)
	go func() {
		_, err := io.CopyN(io.Discard, clientStream, int64(b.N*len(payload)))
		received <- err
	}()
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := serverStream.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := <-received; err != nil {
		b.Fatal(err)
	}
}

func newBenchmarkCover(
	b *testing.B,
	carrier *Conn,
	typeMap protocol.TypeMap,
	engineSeed byte,
	paddingSeed byte,
) *cover.Transport {
	b.Helper()
	engine, err := cover.NewEngine(cover.Config{
		Profile: cover.ProfileWeb, MaxOverheadPercent: 30,
		MaxBudgetBytes: cover.MaxWireCellBytes, Seed: [32]byte{engineSeed},
	})
	if err != nil {
		b.Fatal(err)
	}
	engine.EnableMosaic()
	transport, err := cover.NewTransport(cover.TransportConfig{
		Carrier: carrier, TypeMap: typeMap, Engine: engine,
		PaddingSeed: [32]byte{paddingSeed},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = transport.Close() })
	return transport
}
