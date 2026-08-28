package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cover"
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

func TestAuthenticatedSessionCanBypassCoverTransport(t *testing.T) {
	left, right := newMemoryCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x92, 0x18, 0x73}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		session *Authenticated
		err     error
	}
	serverResult := make(chan result, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test",
			Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
			InitialWindow: 64 * 1024, MaxStreams: 8, DisableCover: true,
		})
		serverResult <- result{session: authenticated, err: err}
	}()
	client, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8, DisableCover: true,
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
	if client.Cover != nil || server.session.Cover != nil {
		t.Fatalf("cover transport installed in fast path: client=%v server=%v", client.Cover, server.session.Cover)
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
	stream, err := client.Mux.Open(ctx, []byte("fast-path"))
	if err != nil {
		t.Fatalf("open fast-path stream: %v", err)
	}
	payload := bytes.Repeat([]byte{0x6a}, 256*1024)
	writeResult := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write(payload)
		if writeErr == nil {
			writeErr = stream.CloseWrite()
		}
		writeResult <- writeErr
	}()
	response, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(response, payload) {
		t.Fatalf("fast-path round trip mismatch: bytes=%d error=%v", len(response), err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("write fast-path stream: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve fast-path stream: %v", err)
	}
}

func TestAuthenticatedSessionCoverModesInteroperateDuringRollingUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name               string
		disableClientCover bool
		disableServerCover bool
	}{
		{name: "new client with covered server", disableClientCover: true},
		{name: "covered client with new server", disableServerCover: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			left, right := newMemoryCarrierPair()
			secret := [protocol.RootSecretSize]byte{0x72, 0x6f, 0x6c, 0x6c}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type result struct {
				session *Authenticated
				err     error
			}
			serverResult := make(chan result, 1)
			go func() {
				authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
					RootSecret: secret, ServerIdentity: "edge.example.test",
					Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
					InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 100,
					DisableCover: tt.disableServerCover,
				})
				serverResult <- result{session: authenticated, err: err}
			}()
			client, err := ConnectClient(ctx, left, AuthenticatedConfig{
				RootSecret: secret, ServerIdentity: "edge.example.test",
				Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
				InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 100,
				DisableCover: tt.disableClientCover,
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
			if (client.Cover == nil) != tt.disableClientCover ||
				(server.session.Cover == nil) != tt.disableServerCover {
				t.Fatalf("unexpected cover layers: client=%v server=%v", client.Cover, server.session.Cover)
			}
			if err := client.Mux.Ping(ctx); err != nil {
				t.Fatalf("client-to-server ping: %v", err)
			}
			if err := server.session.Mux.Ping(ctx); err != nil {
				t.Fatalf("server-to-client ping: %v", err)
			}
		})
	}
}

func TestAuthenticatedSessionInstallsSenderLocalPulse(t *testing.T) {
	for _, tt := range []struct {
		name        string
		clientPulse bool
		serverPulse bool
	}{
		{name: "Pulse client with off server", clientPulse: true},
		{name: "off client with Pulse server", serverPulse: true},
		{name: "Pulse in both directions", clientPulse: true, serverPulse: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			left, right := newMemoryCarrierPair()
			secret := [protocol.RootSecretSize]byte{0x70, 0x75, 0x6c, 0x73, 0x65}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type result struct {
				session *Authenticated
				err     error
			}
			serverResult := make(chan result, 1)
			go func() {
				authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
					RootSecret: secret, ServerIdentity: "edge.example.test",
					Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
					InitialWindow: 64 * 1024, MaxStreams: 8,
					MaxCoverOverheadPercent: 100,
					DisableCover:            !tt.serverPulse, EnablePulse: tt.serverPulse,
				})
				serverResult <- result{session: authenticated, err: err}
			}()
			client, err := ConnectClient(ctx, left, AuthenticatedConfig{
				RootSecret: secret, ServerIdentity: "edge.example.test",
				Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
				InitialWindow: 64 * 1024, MaxStreams: 8,
				MaxCoverOverheadPercent: 100,
				DisableCover:            !tt.clientPulse, EnablePulse: tt.clientPulse,
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
			if client.CoverStats().PulseEnabled != tt.clientPulse ||
				server.session.CoverStats().PulseEnabled != tt.serverPulse {
				t.Fatalf("Pulse direction mismatch: client=%+v server=%+v",
					client.CoverStats(), server.session.CoverStats())
			}
			if client.CoverStats().MosaicEnabled || server.session.CoverStats().MosaicEnabled {
				t.Fatal("Pulse incorrectly enabled negotiated Mosaic")
			}
			if err := client.Mux.Ping(ctx); err != nil {
				t.Fatalf("client-to-server ping: %v", err)
			}
			if err := server.session.Mux.Ping(ctx); err != nil {
				t.Fatalf("server-to-client ping: %v", err)
			}
		})
	}
}

func TestAuthenticatedSessionCarriesDeviceIdentity(t *testing.T) {
	left, right := newMemoryCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	deviceID := protocol.DeviceID{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x01}
	features := protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureDeviceIdentity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverResult := make(chan struct {
		session *Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test", Features: features,
			InitialWindow: 64 * 1024, MaxStreams: 8,
		})
		serverResult <- struct {
			session *Authenticated
			err     error
		}{authenticated, err}
	}()
	client, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test", Features: features,
		DeviceID: deviceID, InitialWindow: 64 * 1024, MaxStreams: 8,
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
	if client.DeviceID != deviceID || server.session.DeviceID != deviceID {
		t.Fatalf("device identities differ: client=%x server=%x", client.DeviceID, server.session.DeviceID)
	}
}

func TestAuthenticatedSessionDeviceIdentityDowngradesForLegacyServer(t *testing.T) {
	left, right := newMemoryCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	deviceID := protocol.DeviceID{0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f, 0x30}
	legacyFeatures := protocol.FeatureMultiplex | protocol.FeatureCellAEAD
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverResult := make(chan struct {
		session *Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, err := AcceptServer(ctx, right, AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test", Features: legacyFeatures,
			InitialWindow: 64 * 1024, MaxStreams: 8,
		})
		serverResult <- struct {
			session *Authenticated
			err     error
		}{authenticated, err}
	}()
	client, err := ConnectClient(ctx, left, AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features: legacyFeatures | protocol.FeatureDeviceIdentity, DeviceID: deviceID,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	})
	if err != nil {
		t.Fatalf("authenticate device-aware client against legacy server: %v", err)
	}
	server := <-serverResult
	if server.err != nil {
		t.Fatalf("authenticate legacy server: %v", server.err)
	}
	t.Cleanup(func() {
		_ = client.Mux.Close()
		_ = server.session.Mux.Close()
	})
	if client.Features != legacyFeatures || server.session.Features != legacyFeatures {
		t.Fatalf("features were not downgraded: client=%v server=%v", client.Features, server.session.Features)
	}
	if !client.DeviceID.IsZero() || !server.session.DeviceID.IsZero() {
		t.Fatalf("legacy session carried device identity: client=%x server=%x", client.DeviceID, server.session.DeviceID)
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
		DisableCover:         true,
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
	if client.Cover != nil || server.Cover != nil {
		t.Fatal("forward-secret fast path unexpectedly installed cover transport")
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
		profileFeature   protocol.FeatureSet
		wantEnabled      bool
	}{
		{name: "both peers", serverExtensions: true, serverMosaic: true,
			profileFeature: protocol.FeatureProfileWeb, wantEnabled: true},
		{name: "v2.2 server without Mosaic", serverExtensions: true, wantEnabled: false},
		{name: "v2.1 server without extensions", serverExtensions: false, wantEnabled: false},
		{name: "quiet profile stays fixed", serverExtensions: true, serverMosaic: true,
			profileFeature: protocol.FeatureProfileQuiet, wantEnabled: false},
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
			profileFeature := tt.profileFeature
			if profileFeature == 0 {
				profileFeature = protocol.FeatureProfileWeb
			}
			serverConfig := AuthenticatedConfig{
				RootSecret: secret, ServerIdentity: "edge.example",
				Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | profileFeature,
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

			clientStats := client.Cover.Stats()
			serverStats := server.Cover.Stats()
			if got := clientStats.MosaicEnabled; got != tt.wantEnabled {
				t.Fatalf("client Mosaic enabled=%v want=%v", got, tt.wantEnabled)
			}
			if got := serverStats.MosaicEnabled; got != tt.wantEnabled {
				t.Fatalf("server Mosaic enabled=%v want=%v", got, tt.wantEnabled)
			}
			if tt.wantEnabled {
				if clientStats.VariantID == 0 || serverStats.VariantID == 0 ||
					clientStats.TrafficClass != cover.TrafficWeb || serverStats.TrafficClass != cover.TrafficWeb {
					t.Fatalf("polymorphic profiles not active: client=%+v server=%+v", clientStats, serverStats)
				}
			} else if clientStats.VariantID != 0 || serverStats.VariantID != 0 {
				t.Fatalf("unselected session derived active variants: client=%+v server=%+v", clientStats, serverStats)
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
