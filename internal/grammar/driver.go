package grammar

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

var (
	ErrLeaseCapacity = errors.New("carrier grammar lease capacity reached")
	ErrLeaseCarrier  = errors.New("unsupported carrier grammar")
	ErrLeaseTime     = errors.New("invalid carrier grammar time")
)

// Lease is declarative carrier lifecycle state. It never contains executable
// callbacks, headers, destinations, payload templates, or user identifiers.
type Lease struct {
	ID               uint64
	Carrier          protocol.CarrierKind
	ExpiresAt        time.Time
	IdleTimeout      time.Duration
	MaxBurstBytes    uint64
	MaxDatagramBytes uint64
}

func (l Lease) ShouldRotate(now, lastActivity time.Time) bool {
	if l.ID == 0 || now.IsZero() || now.Before(lastActivity) {
		return false
	}
	if !now.Before(l.ExpiresAt) {
		return true
	}
	return !lastActivity.IsZero() && l.IdleTimeout > 0 &&
		!now.Before(lastActivity.Add(l.IdleTimeout))
}

func (l Lease) AllowsBurst(bytes uint64) bool {
	return l.ID != 0 && bytes > 0 && bytes <= l.MaxBurstBytes
}

func (l Lease) AllowsDatagram(bytes uint64) bool {
	return l.ID != 0 && l.MaxDatagramBytes > 0 && bytes > 0 && bytes <= l.MaxDatagramBytes
}

// Driver owns the bounded active-lease accounting for one client runtime.
type Driver struct {
	manifest Manifest
	random   io.Reader

	mu     sync.Mutex
	nextID uint64
	leases map[uint64]protocol.CarrierKind
	active map[protocol.CarrierKind]uint8
}

func NewDriver(manifest Manifest, random io.Reader) (*Driver, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if random == nil {
		random = cryptorand.Reader
	}
	return &Driver{
		manifest: manifest, random: random, nextID: 1,
		leases: make(map[uint64]protocol.CarrierKind, 8),
		active: make(map[protocol.CarrierKind]uint8, 3),
	}, nil
}

func (d *Driver) Acquire(carrier protocol.CarrierKind, now time.Time) (Lease, error) {
	if d == nil {
		return Lease{}, ErrManifestInvalid
	}
	stream, err := d.stream(carrier)
	if err != nil {
		return Lease{}, err
	}
	if now.IsZero() {
		return Lease{}, ErrLeaseTime
	}
	lifetime, err := d.sampleLifetime(stream)
	if err != nil {
		return Lease{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active[carrier] >= stream.MaxConcurrent {
		return Lease{}, ErrLeaseCapacity
	}
	id := d.nextID
	d.nextID++
	if id == 0 || d.nextID == 0 {
		return Lease{}, ErrLeaseCapacity
	}
	d.leases[id] = carrier
	d.active[carrier]++
	return Lease{
		ID: id, Carrier: carrier, ExpiresAt: now.Add(lifetime),
		IdleTimeout:   millisDuration(stream.IdleTimeoutMS),
		MaxBurstBytes: stream.MaxBurstBytes, MaxDatagramBytes: stream.MaxDatagramBytes,
	}, nil
}

func (d *Driver) Release(id uint64) bool {
	if d == nil || id == 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	carrier, exists := d.leases[id]
	if !exists {
		return false
	}
	delete(d.leases, id)
	if d.active[carrier] > 1 {
		d.active[carrier]--
	} else {
		delete(d.active, carrier)
	}
	return true
}

func (d *Driver) stream(carrier protocol.CarrierKind) (StreamGrammar, error) {
	switch carrier {
	case protocol.CarrierHTTPS:
		return d.manifest.HTTPS, nil
	case protocol.CarrierHTTP3:
		return d.manifest.HTTP3, nil
	case protocol.CarrierWebRTC:
		return d.manifest.WebRTC, nil
	default:
		return StreamGrammar{}, ErrLeaseCarrier
	}
}

func (d *Driver) sampleLifetime(stream StreamGrammar) (time.Duration, error) {
	minimum := millisDuration(stream.LeaseMinMS)
	maximum := millisDuration(stream.LeaseMaxMS)
	base := minimum + (maximum-minimum)/2
	jitter := time.Duration(uint64(base/time.Millisecond)*uint64(stream.RotateJitterPercent)/100) * time.Millisecond
	lower := max(minimum, base-jitter)
	upper := min(maximum, base+jitter)
	span := upper - lower
	if span <= 0 {
		return lower, nil
	}
	var raw [8]byte
	if _, err := io.ReadFull(d.random, raw[:]); err != nil {
		return 0, err
	}
	offset := time.Duration(binary.BigEndian.Uint64(raw[:]) % uint64(span+1))
	return lower + offset, nil
}
