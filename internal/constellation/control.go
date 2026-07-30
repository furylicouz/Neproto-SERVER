package constellation

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"sync"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const (
	firstContinuityMessageID = 3
	maxContinuityIDAttempts  = 8
)

var (
	ErrContinuityCapability = errors.New("constellation continuity capability unavailable")
	ErrContinuityControl    = errors.New("constellation control exchange failed")
	ErrContinuityState      = errors.New("invalid constellation client state")
)

type ServerControlConfig struct {
	Hub *Hub
}

type ServerControl struct {
	hub *Hub
}

func NewServerControl(config ServerControlConfig) (*ServerControl, error) {
	if config.Hub == nil {
		return nil, ErrHubConfig
	}
	return &ServerControl{hub: config.Hub}, nil
}

// Admit binds one fully authenticated NP/2 session to a logical
// constellation. The caller must not start target proxying before this returns.
func (c *ServerControl) Admit(
	ctx context.Context,
	authenticated *session.Authenticated,
) (*Attachment, error) {
	if c == nil || c.hub == nil || ctx == nil || authenticated == nil || authenticated.Mux == nil {
		return nil, ErrContinuityControl
	}
	if err := requireContinuityCapability(ctx, authenticated); err != nil {
		return nil, err
	}
	principal, err := PrincipalFromCredentialID(authenticated.CredentialID)
	if err != nil {
		return nil, errors.Join(ErrContinuityControl, err)
	}
	transcript, err := TranscriptFromSessionKeys(authenticated.Keys)
	if err != nil {
		return nil, errors.Join(ErrContinuityControl, err)
	}
	leaseKey, err := LeaseKeyFromSessionKeys(authenticated.Keys)
	if err != nil {
		return nil, errors.Join(ErrContinuityControl, err)
	}
	request, err := authenticated.Mux.ReceiveContinuity(ctx)
	if err != nil {
		return nil, errors.Join(ErrContinuityControl, err)
	}
	if request.MessageID >= protocol.MaxSequence {
		return nil, ErrContinuityControl
	}
	var attachment *Attachment
	var responseType protocol.ContinuityMessageType
	switch request.Type {
	case protocol.ContinuityConstellationCreate:
		attachment, err = c.hub.createPending(CreateRequest{
			Principal: principal, ConstellationID: request.ConstellationID,
			Transcript: transcript, LeaseKey: leaseKey,
			Carrier: authenticated.Carrier, Resource: authenticated.Mux,
		})
		responseType = protocol.ContinuityLeaseIssue
	case protocol.ContinuityLeaseAttach:
		ticket, ticketErr := parseLeaseTicket(request.Token)
		if ticketErr != nil {
			return nil, ticketErr
		}
		attachment, err = c.hub.attachPending(AttachRequest{
			Ticket: ticket, Principal: principal, ConstellationID: request.ConstellationID,
			Transcript: transcript, LeaseKey: leaseKey,
			Carrier: authenticated.Carrier, Resource: authenticated.Mux,
		})
		responseType = protocol.ContinuityLeaseAccept
	default:
		return nil, ErrContinuityControl
	}
	if err != nil {
		return nil, err
	}
	response := protocol.ContinuityFrame{
		Type: responseType, MessageID: request.MessageID,
		ConstellationID: request.ConstellationID,
		Token:           append([]byte(nil), attachment.Ticket[:]...),
	}
	if err := authenticated.Mux.SendContinuity(ctx, response); err != nil {
		_ = attachment.Close()
		return nil, errors.Join(ErrContinuityControl, err)
	}
	attachment.commitAdmission()
	attachment.ControlNextMessageID = request.MessageID + 1
	return attachment, nil
}

type ClientControlState struct {
	Ready           bool
	ConstellationID protocol.ContinuityID
	LeaseKey        protocol.ContinuityID
	NextMessageID   uint64
}

type ClientControl struct {
	mu sync.Mutex

	random          io.Reader
	constellationID protocol.ContinuityID
	leaseKey        protocol.ContinuityID
	ticket          continuity.LeaseTicket
	nextMessageID   uint64
}

func NewClientControl(random io.Reader) (*ClientControl, error) {
	if random == nil {
		random = cryptorand.Reader
	}
	return &ClientControl{random: random, nextMessageID: firstContinuityMessageID}, nil
}

func (c *ClientControl) Create(ctx context.Context, authenticated *session.Authenticated) error {
	if c == nil || ctx == nil || authenticated == nil || authenticated.Mux == nil {
		return ErrContinuityControl
	}
	if err := requireContinuityCapability(ctx, authenticated); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if parameters, negotiated := authenticated.Extensions(); negotiated &&
		parameters.Capabilities&protocol.CapabilityForwardSecrecy != 0 &&
		c.nextMessageID == firstContinuityMessageID {
		c.nextMessageID++
	}
	if c.constellationID != (protocol.ContinuityID{}) || c.ticket != (continuity.LeaseTicket{}) {
		return ErrContinuityState
	}
	constellationID, err := randomContinuityID(c.random)
	if err != nil {
		return err
	}
	leaseKey, err := LeaseKeyFromSessionKeys(authenticated.Keys)
	if err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	messageID, err := c.takeMessageIDLocked()
	if err != nil {
		return err
	}
	if err := authenticated.Mux.SendContinuity(ctx, protocol.ContinuityFrame{
		Type: protocol.ContinuityConstellationCreate, MessageID: messageID,
		ConstellationID: constellationID,
	}); err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	response, err := authenticated.Mux.ReceiveContinuity(ctx)
	if err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	if response.Type != protocol.ContinuityLeaseIssue || response.MessageID != messageID ||
		response.ConstellationID != constellationID {
		return ErrContinuityControl
	}
	ticket, err := parseLeaseTicket(response.Token)
	if err != nil {
		return err
	}
	c.constellationID = constellationID
	c.leaseKey = leaseKey
	c.ticket = ticket
	return nil
}

func (c *ClientControl) Attach(ctx context.Context, authenticated *session.Authenticated) error {
	if c == nil || ctx == nil || authenticated == nil || authenticated.Mux == nil {
		return ErrContinuityControl
	}
	if err := requireContinuityCapability(ctx, authenticated); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.constellationID == (protocol.ContinuityID{}) || c.ticket == (continuity.LeaseTicket{}) {
		return ErrContinuityState
	}
	messageID, err := c.takeMessageIDLocked()
	if err != nil {
		return err
	}
	leaseKey, err := LeaseKeyFromSessionKeys(authenticated.Keys)
	if err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	if err := authenticated.Mux.SendContinuity(ctx, protocol.ContinuityFrame{
		Type: protocol.ContinuityLeaseAttach, MessageID: messageID,
		ConstellationID: c.constellationID,
		Token:           append([]byte(nil), c.ticket[:]...),
	}); err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	response, err := authenticated.Mux.ReceiveContinuity(ctx)
	if err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	if response.Type != protocol.ContinuityLeaseAccept || response.MessageID != messageID ||
		response.ConstellationID != c.constellationID {
		return ErrContinuityControl
	}
	nextTicket, err := parseLeaseTicket(response.Token)
	if err != nil {
		return err
	}
	c.ticket = nextTicket
	c.leaseKey = leaseKey
	return nil
}

func (c *ClientControl) State() ClientControlState {
	if c == nil {
		return ClientControlState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ClientControlState{
		Ready:           c.constellationID != (protocol.ContinuityID{}) && c.ticket != (continuity.LeaseTicket{}),
		ConstellationID: c.constellationID, LeaseKey: c.leaseKey,
		NextMessageID: c.nextMessageID,
	}
}

func (c *ClientControl) takeMessageIDLocked() (uint64, error) {
	if c.nextMessageID < firstContinuityMessageID || c.nextMessageID > protocol.MaxSequence {
		return 0, ErrContinuityState
	}
	id := c.nextMessageID
	c.nextMessageID++
	return id, nil
}

func requireContinuityCapability(
	ctx context.Context,
	authenticated *session.Authenticated,
) error {
	parameters, negotiated, err := authenticated.WaitExtensions(ctx)
	if err != nil {
		return err
	}
	if !negotiated || parameters.Capabilities&protocol.CapabilityConstellationContinuity == 0 {
		return ErrContinuityCapability
	}
	return nil
}

func parseLeaseTicket(raw []byte) (continuity.LeaseTicket, error) {
	if len(raw) != continuity.LeaseTicketSize {
		return continuity.LeaseTicket{}, ErrContinuityControl
	}
	var ticket continuity.LeaseTicket
	copy(ticket[:], raw)
	if ticket == (continuity.LeaseTicket{}) {
		return continuity.LeaseTicket{}, ErrContinuityControl
	}
	return ticket, nil
}

func randomContinuityID(random io.Reader) (protocol.ContinuityID, error) {
	if random == nil {
		return protocol.ContinuityID{}, ErrContinuityControl
	}
	for range maxContinuityIDAttempts {
		var id protocol.ContinuityID
		if _, err := io.ReadFull(random, id[:]); err != nil {
			return protocol.ContinuityID{}, errors.Join(ErrContinuityControl, err)
		}
		if id != (protocol.ContinuityID{}) {
			return id, nil
		}
	}
	return protocol.ContinuityID{}, ErrContinuityControl
}
