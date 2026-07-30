package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestAuthenticatedSessionCarriesStream(t *testing.T) {
	left, right := newMemoryCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serverResult := make(chan struct {
		session *Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test",
			Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
			InitialWindow: 64 * 1024, MaxStreams: 8,
			MaxCoverOverheadPercent: 30,
		})
		serverResult <- struct {
			session *Authenticated
			err     error
		}{authenticated, err}
	}()
	client, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		MaxCoverOverheadPercent: 30,
	})
	if err != nil {
		t.Fatalf("authenticate client: %v", err)
	}
	server := <-serverResult
	if server.err != nil {
		t.Fatalf("authenticate server: %v", server.err)
	}
	t.Cleanup(func() {
		_ = client.Mux.Close()
		_ = server.session.Mux.Close()
	})
	if client.Keys != server.session.Keys {
		t.Fatal("peers derived different session keys")
	}
	if client.Features != server.session.Features {
		t.Fatal("peers negotiated different features")
	}
	if client.Cover == nil || server.session.Cover == nil {
		t.Fatal("authenticated session did not install cover transport")
	}

	served := make(chan error, 1)
	go func() {
		incoming, acceptErr := server.session.Mux.Accept(ctx)
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		stream, acceptErr := incoming.Accept()
		if acceptErr == nil {
			_, acceptErr = io.Copy(stream, stream)
		}
		if acceptErr == nil {
			acceptErr = stream.CloseWrite()
		}
		served <- acceptErr
	}()
	stream, err := client.Mux.Open(ctx, []byte("authenticated"))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	// Exceed the initial flow-control window by a wide margin. Reading and
	// writing concurrently matches a browser's full-duplex TCP behavior and
	// catches the "first packets work, then stall" failure mode.
	payload := bytes.Repeat([]byte{0x5c}, 1024*1024)
	writeResult := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write(payload)
		if writeErr == nil {
			writeErr = stream.CloseWrite()
		}
		writeResult <- writeErr
	}()
	response, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatal("authenticated stream payload mismatch")
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("write long-lived stream: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve stream: %v", err)
	}
	if stats := client.Cover.Stats(); stats.RealMessages == 0 || stats.PaddingBytes == 0 {
		t.Fatalf("web cover was not applied to client wire cells: %#v", stats)
	} else if stats.ActualOverheadBytes()*100 > stats.RealWireBytes*30 {
		t.Fatalf("actual client cover exceeded negotiated budget: %#v", stats)
	}
}

func TestAuthenticatedSessionRejectsWrongSecret(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		_, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: [protocol.RootSecretSize]byte{0x11}, ServerIdentity: "edge.example.test",
			Features: protocol.FeatureMultiplex | protocol.FeatureCellAEAD, InitialWindow: 4096, MaxStreams: 2,
		})
		serverErr <- err
	}()
	_, clientErr := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: [protocol.RootSecretSize]byte{0x22}, ServerIdentity: "edge.example.test",
		Features: protocol.FeatureMultiplex | protocol.FeatureCellAEAD, InitialWindow: 4096, MaxStreams: 2,
	})
	if clientErr == nil {
		t.Fatal("client accepted wrong secret")
	}
	if err := <-serverErr; !errors.Is(err, protocol.ErrAuthentication) {
		t.Fatalf("server expected authentication error, got %v", err)
	}
}

func TestAuthenticatedServerAcceptsIndependentCredentialAndReportsLocalID(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clientSecret := [protocol.RootSecretSize]byte{0x51, 0x52}
	serverResult := make(chan struct {
		session *Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
			Credentials: []ServerCredential{
				{ID: "bob", RootSecret: [protocol.RootSecretSize]byte{0x21}},
				{ID: "alice", RootSecret: clientSecret},
			},
			ServerIdentity: "edge.example.test",
			Features:       protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
			InitialWindow:  4096, MaxStreams: 2,
		})
		serverResult <- struct {
			session *Authenticated
			err     error
		}{authenticated, err}
	}()
	client, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: clientSecret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 4096, MaxStreams: 2,
	})
	if err != nil {
		t.Fatalf("authenticate independent client: %v", err)
	}
	server := <-serverResult
	if server.err != nil {
		t.Fatalf("accept independent credential: %v", server.err)
	}
	t.Cleanup(func() {
		_ = client.Mux.Close()
		_ = server.session.Mux.Close()
	})
	if server.session.CredentialID != "alice" {
		t.Fatalf("server credential ID=%q", server.session.CredentialID)
	}
}

func TestAuthenticatedSessionRejectsUnsupportedFeatures(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	secret := [protocol.RootSecretSize]byte{0x33}
	serverErr := make(chan error, 1)
	go func() {
		_, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test",
			Features: protocol.FeatureMultiplex | protocol.FeatureCellAEAD, InitialWindow: 4096, MaxStreams: 2,
		})
		serverErr <- err
	}()
	_, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileInteractive,
		InitialWindow: 4096, MaxStreams: 2,
	})
	if !errors.Is(err, protocol.ErrUnsupportedFeatures) {
		t.Fatalf("expected unsupported features, got %v", err)
	}
	if err := <-serverErr; err == nil {
		t.Fatal("server survived client handshake rejection")
	}
}

func TestAuthenticatedSessionNegotiatesProductionExtensions(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := productionExtensionParameters()
	request := offer
	request.MaxUDPAssociations = 64
	request.MaxStreamWindowBytes = 4 * 1024 * 1024
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	serverConfig := AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionOffer: &offer, ExtensionTimeout: time.Second,
	}
	clientConfig := serverConfig
	clientConfig.ExtensionOffer = nil
	clientConfig.ExtensionRequest = &request
	clientConfig.RequiredExtensions = protocol.CapabilityReliableUDP

	serverResult := make(chan *Authenticated, 1)
	serverErr := make(chan error, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, left, serverConfig)
		serverResult <- authenticated
		serverErr <- err
	}()
	client, err := ConnectClient(ctx, right, clientConfig)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = client.Mux.Close() })
	server := <-serverResult
	if err := <-serverErr; err != nil {
		t.Fatalf("accept server: %v", err)
	}
	t.Cleanup(func() { _ = server.Mux.Close() })

	clientParameters, negotiated, err := client.WaitExtensions(ctx)
	if err != nil || !negotiated || clientParameters != request {
		t.Fatalf("client extensions=%+v negotiated=%v err=%v", clientParameters, negotiated, err)
	}
	serverParameters, negotiated, err := server.WaitExtensions(ctx)
	if err != nil || !negotiated || serverParameters != request {
		t.Fatalf("server extensions=%+v negotiated=%v err=%v", serverParameters, negotiated, err)
	}
}

func TestAuthenticatedSessionRekeysWithX25519BeforeApplicationCells(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := productionExtensionParameters()
	request := offer
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	serverConfig := AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionOffer: &offer, ExtensionTimeout: time.Second,
		EnableForwardSecrecy: true,
	}
	clientConfig := serverConfig
	clientConfig.ExtensionOffer = nil
	clientConfig.ExtensionRequest = &request
	serverResult := make(chan *Authenticated, 1)
	serverErr := make(chan error, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, left, serverConfig)
		serverResult <- authenticated
		serverErr <- err
	}()
	client, err := ConnectClient(ctx, right, clientConfig)
	if err != nil {
		t.Fatalf("connect forward-secret client: %v", err)
	}
	server := <-serverResult
	if err := <-serverErr; err != nil {
		t.Fatalf("accept forward-secret server: %v", err)
	}
	defer client.Mux.Close()
	defer server.Mux.Close()
	serverExtensions, negotiated, err := server.WaitExtensions(ctx)
	if err != nil || !negotiated || serverExtensions.Capabilities&protocol.CapabilityForwardSecrecy == 0 {
		t.Fatalf("server extensions=%+v negotiated=%v err=%v", serverExtensions, negotiated, err)
	}
	clientExtensions, negotiated, err := client.WaitExtensions(ctx)
	if err != nil || !negotiated || clientExtensions.Capabilities&protocol.CapabilityForwardSecrecy == 0 {
		t.Fatalf("client extensions=%+v negotiated=%v err=%v", clientExtensions, negotiated, err)
	}
	if client.Keys != server.Keys || client.Keys.Control == ([32]byte{}) ||
		clientExtensions.ForwardSecretKeyShare == ([32]byte{}) {
		t.Fatal("forward-secret session keys or key share mismatch")
	}
	served := make(chan error, 1)
	go func() {
		incoming, acceptErr := server.Mux.Accept(ctx)
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		stream, acceptErr := incoming.Accept()
		if acceptErr == nil {
			_, acceptErr = io.Copy(stream, stream)
		}
		served <- acceptErr
	}()
	stream, err := client.Mux.Open(ctx, []byte("post-x25519"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("protected")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("protected"))
	if _, err := io.ReadFull(stream, response); err != nil || string(response) != "protected" {
		t.Fatalf("response=%q err=%v", response, err)
	}
	_ = stream.Close()
}

func TestAuthenticatedSessionRejectsForwardSecrecyDowngrade(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := productionExtensionParameters()
	request := offer
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	serverConfig := AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionOffer: &offer, ExtensionTimeout: time.Second,
	}
	clientConfig := serverConfig
	clientConfig.ExtensionOffer = nil
	clientConfig.ExtensionRequest = &request
	clientConfig.EnableForwardSecrecy = true
	go func() {
		authenticated, _ := AcceptServer(ctx, left, serverConfig)
		if authenticated != nil {
			_, _, _ = authenticated.WaitExtensions(ctx)
			_ = authenticated.Mux.Close()
		}
	}()
	client, err := ConnectClient(ctx, right, clientConfig)
	if client != nil || !errors.Is(err, ErrRequiredExtension) {
		t.Fatalf("downgraded client=%v err=%v", client, err)
	}
}

func TestAuthenticatedSessionActivatesMosaicOnlyWhenNegotiated(t *testing.T) {
	tests := []struct {
		name             string
		serverExtensions bool
		serverMosaic     bool
		wantEnabled      bool
	}{
		{name: "both peers", serverExtensions: true, serverMosaic: true, wantEnabled: true},
		{name: "v2.2 server without Mosaic", serverExtensions: true, wantEnabled: false},
		{name: "v2.1 server without extensions", serverExtensions: false, wantEnabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right := newMemoryCarrierPair()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			offer := productionExtensionParameters()
			request := offer
			request.Capabilities |= protocol.CapabilityMosaicCover
			if tt.serverMosaic {
				offer.Capabilities |= protocol.CapabilityMosaicCover
			} else {
				offer.Capabilities &^= protocol.CapabilityMosaicCover
			}
			secret := [protocol.RootSecretSize]byte{0x6d, 0x6f, 0x73, 0x61, 0x69, 0x63}
			serverConfig := AuthenticatedConfig{
				RootSecret: secret, ServerIdentity: "edge.example",
				Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
				InitialWindow: 64 * 1024, MaxStreams: 8,
				ExtensionTimeout: 100 * time.Millisecond,
			}
			if tt.serverExtensions {
				serverConfig.ExtensionOffer = &offer
			}
			clientConfig := serverConfig
			clientConfig.ExtensionOffer = nil
			clientConfig.ExtensionRequest = &request

			serverResult := make(chan *Authenticated, 1)
			serverErr := make(chan error, 1)
			go func() {
				authenticated, err := AcceptServer(ctx, left, serverConfig)
				serverResult <- authenticated
				serverErr <- err
			}()
			client, err := ConnectClient(ctx, right, clientConfig)
			if err != nil {
				t.Fatalf("connect client: %v", err)
			}
			t.Cleanup(func() { _ = client.Mux.Close() })
			server := <-serverResult
			if err := <-serverErr; err != nil {
				t.Fatalf("accept server: %v", err)
			}
			t.Cleanup(func() { _ = server.Mux.Close() })
			if _, _, err := server.WaitExtensions(ctx); err != nil {
				t.Fatalf("wait server extensions: %v", err)
			}

			if got := client.Cover.Stats().MosaicEnabled; got != tt.wantEnabled {
				t.Fatalf("client Mosaic enabled=%v want=%v", got, tt.wantEnabled)
			}
			if got := server.Cover.Stats().MosaicEnabled; got != tt.wantEnabled {
				t.Fatalf("server Mosaic enabled=%v want=%v", got, tt.wantEnabled)
			}
		})
	}
}

func TestAuthenticatedClientFailsRequiredExtensionsBeforeUse(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := productionExtensionParameters()
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	serverConfig := AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	}
	clientConfig := serverConfig
	clientConfig.ExtensionRequest = &request
	clientConfig.RequiredExtensions = protocol.CapabilityReliableUDP
	clientConfig.ExtensionTimeout = 25 * time.Millisecond

	serverDone := make(chan *Authenticated, 1)
	go func() {
		authenticated, _ := AcceptServer(ctx, left, serverConfig)
		serverDone <- authenticated
	}()
	client, err := ConnectClient(ctx, right, clientConfig)
	if client != nil || !errors.Is(err, ErrExtensionNegotiation) {
		t.Fatalf("client=%v required extension error=%v", client, err)
	}
	if server := <-serverDone; server != nil {
		_ = server.Mux.Close()
	}
}

func TestAuthenticatedSessionRequiresCellAEAD(t *testing.T) {
	left, right := newMemoryCarrierPair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	secret := [protocol.RootSecretSize]byte{0x44}
	serverErr := make(chan error, 1)
	go func() {
		_, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test",
			Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
			InitialWindow: 4096, MaxStreams: 2,
		})
		serverErr <- err
	}()
	_, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features: protocol.FeatureMultiplex, InitialWindow: 4096, MaxStreams: 2,
	})
	if !errors.Is(err, protocol.ErrUnsupportedFeatures) {
		t.Fatalf("expected mandatory cell encryption failure, got %v", err)
	}
	if err := <-serverErr; !errors.Is(err, protocol.ErrUnsupportedFeatures) {
		t.Fatalf("server accepted a client without cell encryption: %v", err)
	}
}
