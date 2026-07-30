package grammar

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	ManifestVersion = 1
	MaxManifestSize = 16 * 1024

	minLeaseLifetime = 5 * time.Second
	maxLeaseLifetime = 30 * time.Minute
	minIdleTimeout   = time.Second
	maxIdleTimeout   = 5 * time.Minute
	minBurstBytes    = 16 * 1024
	maxBurstBytes    = 8 * 1024 * 1024
)

var (
	ErrManifestInvalid = errors.New("invalid carrier grammar manifest")
	ErrManifestSize    = errors.New("carrier grammar manifest exceeds size limit")
)

// Manifest is bounded declarative data. It cannot name code, commands,
// destinations, headers, or arbitrary byte templates.
type Manifest struct {
	Version uint8         `json:"version"`
	HTTPS   StreamGrammar `json:"https"`
	HTTP3   StreamGrammar `json:"http3"`
	WebRTC  StreamGrammar `json:"webrtc"`
}

type StreamGrammar struct {
	LeaseMinMS          uint64 `json:"lease_min_ms"`
	LeaseMaxMS          uint64 `json:"lease_max_ms"`
	IdleTimeoutMS       uint64 `json:"idle_timeout_ms"`
	MaxConcurrent       uint8  `json:"max_concurrent"`
	MaxBurstBytes       uint64 `json:"max_burst_bytes"`
	RotateJitterPercent uint8  `json:"rotate_jitter_percent"`
	MaxDatagramBytes    uint64 `json:"max_datagram_bytes"`
}

func DefaultManifest() Manifest {
	return Manifest{
		Version: ManifestVersion,
		HTTPS: StreamGrammar{
			LeaseMinMS: 90_000, LeaseMaxMS: 180_000, IdleTimeoutMS: 60_000,
			MaxConcurrent: 3, MaxBurstBytes: 1024 * 1024, RotateJitterPercent: 20,
		},
		HTTP3: StreamGrammar{
			LeaseMinMS: 120_000, LeaseMaxMS: 300_000, IdleTimeoutMS: 45_000,
			MaxConcurrent: 3, MaxBurstBytes: 2 * 1024 * 1024, RotateJitterPercent: 20,
			MaxDatagramBytes: 65_507,
		},
		WebRTC: StreamGrammar{
			LeaseMinMS: 120_000, LeaseMaxMS: 300_000, IdleTimeoutMS: 60_000,
			MaxConcurrent: 2, MaxBurstBytes: 512 * 1024, RotateJitterPercent: 25,
			MaxDatagramBytes: 16_384,
		},
	}
}

func Parse(raw []byte) (Manifest, error) {
	if len(raw) == 0 {
		return Manifest{}, ErrManifestInvalid
	}
	if len(raw) > MaxManifestSize {
		return Manifest{}, ErrManifestSize
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errors.Join(ErrManifestInvalid, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) MarshalBinary() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Join(ErrManifestInvalid, err)
	}
	if len(raw) > MaxManifestSize {
		return nil, ErrManifestSize
	}
	return raw, nil
}

func (m Manifest) Validate() error {
	if m.Version != ManifestVersion ||
		validateStream(m.HTTPS, false) != nil ||
		validateStream(m.HTTP3, true) != nil ||
		validateStream(m.WebRTC, true) != nil {
		return ErrManifestInvalid
	}
	return nil
}

func validateStream(grammar StreamGrammar, datagrams bool) error {
	minimum := millisDuration(grammar.LeaseMinMS)
	maximum := millisDuration(grammar.LeaseMaxMS)
	idle := millisDuration(grammar.IdleTimeoutMS)
	if minimum < minLeaseLifetime || maximum > maxLeaseLifetime || minimum > maximum ||
		idle < minIdleTimeout || idle > maxIdleTimeout || idle > maximum ||
		grammar.MaxConcurrent == 0 || grammar.MaxConcurrent > 3 ||
		grammar.MaxBurstBytes < minBurstBytes || grammar.MaxBurstBytes > maxBurstBytes ||
		grammar.RotateJitterPercent > 30 {
		return ErrManifestInvalid
	}
	if !datagrams && grammar.MaxDatagramBytes != 0 {
		return ErrManifestInvalid
	}
	if datagrams && (grammar.MaxDatagramBytes < 512 || grammar.MaxDatagramBytes > 65_507) {
		return ErrManifestInvalid
	}
	return nil
}

func millisDuration(value uint64) time.Duration {
	if value > uint64(maxLeaseLifetime/time.Millisecond) {
		return maxLeaseLifetime + time.Millisecond
	}
	return time.Duration(value) * time.Millisecond
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrManifestInvalid
	}
	return nil
}
