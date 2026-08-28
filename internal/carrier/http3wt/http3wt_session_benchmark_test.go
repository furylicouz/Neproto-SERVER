package http3wt

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func BenchmarkSessionDownload(b *testing.B) {
	for _, benchmark := range []struct {
		name        string
		payloadSize int
		cover       bool
	}{
		{name: "fast-path-1400", payloadSize: 1400},
		{name: "mosaic-1400", payloadSize: 1400, cover: true},
		{name: "fast-path-20k", payloadSize: 20 * 1024},
		{name: "mosaic-20k", payloadSize: 20 * 1024, cover: true},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkSessionDownload(b, benchmark.payloadSize, benchmark.cover)
		})
	}
}

func benchmarkSessionDownload(b *testing.B, payloadSize int, withCover bool) {
	_, clientCarrier, serverCarrier := newCarrierPair(b, 1)
	typeMap, err := protocol.NewTypeMap([32]byte{0xd1, 0x27})
	if err != nil {
		b.Fatal(err)
	}
	var clientTransport carrier.Carrier = clientCarrier
	var serverTransport carrier.Carrier = serverCarrier
	if withCover {
		clientTransport = newBenchmarkCover(b, clientCarrier, typeMap, 0x31, 0x41)
		serverTransport = newBenchmarkCover(b, serverCarrier, typeMap, 0x32, 0x42)
	}
	clientMux, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: clientTransport, TypeMap: typeMap,
		InitialWindow: 2 * 1024 * 1024, MaxStreams: 128,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = clientMux.Close() })
	serverMux, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: serverTransport, TypeMap: typeMap,
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

	payload := bytes.Repeat([]byte{0xa5}, payloadSize)
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
