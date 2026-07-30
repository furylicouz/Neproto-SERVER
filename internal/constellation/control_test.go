package constellation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestControlPlaneCreatesAndAttachesIndependentAuthenticatedLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 2, 3, ticketBytes(1, 2, 3, 4))
	t.Cleanup(func() { _ = hub.Close() })
	serverControl, err := NewServerControl(ServerControlConfig{Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	clientControl, err := NewClientControl(bytes.NewReader(ticketBytes(17)))
	if err != nil {
		t.Fatal(err)
	}

	firstClient, firstResult := startControlServer(t, ctx, serverControl, true)
	if err := clientControl.Create(ctx, firstClient); err != nil {
		first := <-firstResult
		t.Fatalf("client create: %v; server admit: %v", err, first.err)
	}
	first := <-firstResult
	if first.err != nil || first.attachment == nil || !first.attachment.Primary {
		t.Fatalf("first attachment=%+v err=%v", first.attachment, first.err)
	}
	state := clientControl.State()
	if !state.Ready || state.ConstellationID == (protocol.ContinuityID{}) ||
		state.LeaseKey == (protocol.ContinuityID{}) {
		t.Fatalf("client state=%+v", state)
	}
	firstLeaseKey := state.LeaseKey
	if first.attachment.LeaseKey != firstLeaseKey ||
		first.attachment.ControlNextMessageID != state.NextMessageID {
		t.Fatalf("first attachment=%+v client state=%+v", first.attachment, state)
	}

	secondClient, secondResult := startControlServer(t, ctx, serverControl, true)
	if err := clientControl.Attach(ctx, secondClient); err != nil {
		t.Fatalf("client attach: %v", err)
	}
	second := <-secondResult
	if second.err != nil || second.attachment == nil || second.attachment.Primary {
		t.Fatalf("second attachment=%+v err=%v", second.attachment, second.err)
	}
	if attachedState := clientControl.State(); second.attachment.LeaseKey != attachedState.LeaseKey ||
		second.attachment.ControlNextMessageID != attachedState.NextMessageID {
		t.Fatalf("second attachment=%+v client state=%+v", second.attachment, attachedState)
	}
	if serverState, ok := hub.State(state.ConstellationID); !ok || serverState.Active != 2 {
		t.Fatalf("server state=%+v ok=%v", serverState, ok)
	}
	if attachedState := clientControl.State(); attachedState.LeaseKey == firstLeaseKey {
		t.Fatalf("replacement session reused lease key: %+v", attachedState)
	}

	_ = first.attachment.Close()
	_ = second.attachment.Close()
	_ = firstClient.Mux.Close()
	_ = secondClient.Mux.Close()
}

func TestClientControlRequiresNegotiatedConstellationCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, server := authenticatedControlPair(t, ctx, false)
	t.Cleanup(func() {
		_ = client.Mux.Close()
		_ = server.Mux.Close()
	})
	control, err := NewClientControl(bytes.NewReader(ticketBytes(17)))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Create(ctx, client); !errors.Is(err, ErrContinuityCapability) {
		t.Fatalf("missing capability error=%v", err)
	}
}

func TestContinuityIdentityDerivationIsStableAndDomainSeparated(t *testing.T) {
	principal, err := PrincipalFromCredentialID("alice")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := PrincipalFromCredentialID("alice")
	other, _ := PrincipalFromCredentialID("bob")
	if principal == (continuity.PrincipalID{}) || principal != again || principal == other {
		t.Fatalf("principal=%x again=%x other=%x", principal, again, other)
	}
	keys := protocol.SessionKeys{Control: [32]byte{1, 2, 3}}
	transcript, err := TranscriptFromSessionKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	keys.HeaderMap = keys.Control
	otherFieldOnly, _ := TranscriptFromSessionKeys(protocol.SessionKeys{Control: [32]byte{1, 2, 3}, HeaderMap: [32]byte{9}})
	if transcript == (continuity.TranscriptID{}) || transcript != otherFieldOnly {
		t.Fatalf("transcript=%x other-field=%x", transcript, otherFieldOnly)
	}
	if _, err := PrincipalFromCredentialID(""); !errors.Is(err, ErrContinuityIdentity) {
		t.Fatalf("empty credential error=%v", err)
	}
	if _, err := TranscriptFromSessionKeys(protocol.SessionKeys{}); !errors.Is(err, ErrContinuityIdentity) {
		t.Fatalf("zero keys error=%v", err)
	}
	leaseKey, err := LeaseKeyFromSessionKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	againLeaseKey, _ := LeaseKeyFromSessionKeys(keys)
	otherLeaseKey, _ := LeaseKeyFromSessionKeys(protocol.SessionKeys{Control: [32]byte{2, 3, 4}})
	if leaseKey == (protocol.ContinuityID{}) || leaseKey != againLeaseKey || leaseKey == otherLeaseKey {
		t.Fatalf("lease key=%x again=%x other=%x", leaseKey, againLeaseKey, otherLeaseKey)
	}
}

type controlServerResult struct {
	attachment *Attachment
	err        error
}

func startControlServer(
	t *testing.T,
	ctx context.Context,
	control *ServerControl,
	capability bool,
) (*session.Authenticated, <-chan controlServerResult) {
	t.Helper()
	client, server := authenticatedControlPair(t, ctx, capability)
	result := make(chan controlServerResult, 1)
	go func() {
		attachment, err := control.Admit(ctx, server)
		result <- controlServerResult{attachment: attachment, err: err}
	}()
	return client, result
}

func authenticatedControlPair(
	t *testing.T,
	ctx context.Context,
	capability bool,
) (*session.Authenticated, *session.Authenticated) {
	t.Helper()
	left, right := newControlMemoryCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	extensions := controlExtensionParameters(capability)
	serverResult := make(chan struct {
		authenticated *session.Authenticated
		err           error
	}, 1)
	go func() {
		authenticated, err := session.AcceptServer(ctx, right, session.AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example",
			Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
			InitialWindow: 64 * 1024, MaxStreams: 8,
			ExtensionOffer: &extensions, ExtensionTimeout: time.Second,
		})
		serverResult <- struct {
			authenticated *session.Authenticated
			err           error
		}{authenticated: authenticated, err: err}
	}()
	client, err := session.ConnectClient(ctx, left, session.AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionRequest: &extensions, ExtensionTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	server := <-serverResult
	if server.err != nil {
		t.Fatalf("accept server: %v", server.err)
	}
	if _, _, err := server.authenticated.WaitExtensions(ctx); err != nil {
		t.Fatalf("server extensions: %v", err)
	}
	return client, server.authenticated
}

func controlExtensionParameters(capability bool) protocol.ExtensionParameters {
	capabilities := protocol.CapabilityAdaptiveWindow
	if capability {
		capabilities |= protocol.CapabilityConstellationContinuity
	}
	return protocol.ExtensionParameters{
		Capabilities:           capabilities,
		MaxSessionReceiveBytes: 8 * 1024 * 1024,
		MaxStreamWindowBytes:   1024 * 1024,
	}
}

type controlMemoryCarrier struct {
	kind     protocol.CarrierKind
	send     chan<- []byte
	receive  <-chan []byte
	done     chan struct{}
	peerDone <-chan struct{}
	once     sync.Once
}

var _ carrier.Carrier = (*controlMemoryCarrier)(nil)

func newControlMemoryCarrierPair() (*controlMemoryCarrier, *controlMemoryCarrier) {
	leftToRight := make(chan []byte, 64)
	rightToLeft := make(chan []byte, 64)
	leftDone := make(chan struct{})
	rightDone := make(chan struct{})
	left := &controlMemoryCarrier{
		kind: protocol.CarrierHTTPS, send: leftToRight, receive: rightToLeft,
		done: leftDone, peerDone: rightDone,
	}
	right := &controlMemoryCarrier{
		kind: protocol.CarrierHTTPS, send: rightToLeft, receive: leftToRight,
		done: rightDone, peerDone: leftDone,
	}
	return left, right
}

func (c *controlMemoryCarrier) Send(ctx context.Context, raw []byte) error {
	payload := append([]byte(nil), raw...)
	select {
	case c.send <- payload:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.ErrClosedPipe
	case <-c.peerDone:
		return io.EOF
	}
}

func (c *controlMemoryCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case payload := <-c.receive:
		return append([]byte(nil), payload...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.ErrClosedPipe
	case <-c.peerDone:
		return nil, io.EOF
	}
}

func (c *controlMemoryCarrier) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *controlMemoryCarrier) Kind() protocol.CarrierKind { return c.kind }
