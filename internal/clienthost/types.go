package clienthost

const (
	HostAPIMajor = 1
	HostAPIMinor = 0
)

type State string

const (
	StateUnknown       State = "unknown"
	StateDisconnected  State = "disconnected"
	StateConnecting    State = "connecting"
	StateConnected     State = "connected"
	StateReconnecting  State = "reconnecting"
	StateDisconnecting State = "disconnecting"
	StateFailed        State = "failed"
)

type Carrier string

const (
	CarrierUnknown           Carrier = "unknown"
	CarrierNone              Carrier = "none"
	CarrierHTTP3WebTransport Carrier = "http3_webtransport"
)

type Snapshot struct {
	State                  State        `json:"state"`
	ProfileID              string       `json:"profile_id,omitempty"`
	Carrier                Carrier      `json:"carrier"`
	ConnectedAtUnixMS      int64        `json:"connected_at_unix_ms,omitempty"`
	UploadBytesPerSecond   int64        `json:"upload_bytes_per_second"`
	DownloadBytesPerSecond int64        `json:"download_bytes_per_second"`
	UploadTotalBytes       int64        `json:"upload_total_bytes"`
	DownloadTotalBytes     int64        `json:"download_total_bytes"`
	Sequence               int64        `json:"sequence"`
	LastError              *PublicError `json:"last_error,omitempty"`
}

func (s Snapshot) Validate() error {
	if !validState(s.State) || !validCarrier(s.Carrier) || s.Sequence < 0 ||
		s.ConnectedAtUnixMS < 0 || s.UploadBytesPerSecond < 0 ||
		s.DownloadBytesPerSecond < 0 || s.UploadTotalBytes < 0 || s.DownloadTotalBytes < 0 {
		return ErrInvalidInput
	}
	if (s.State == StateConnected || s.State == StateReconnecting) &&
		s.Carrier != CarrierHTTP3WebTransport {
		return ErrInvalidInput
	}
	if s.State == StateDisconnected && s.Carrier != CarrierNone {
		return ErrInvalidInput
	}
	if s.LastError != nil && s.LastError.Validate() != nil {
		return ErrInvalidInput
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StateDisconnected, StateConnecting, StateConnected, StateReconnecting,
		StateDisconnecting, StateFailed:
		return true
	default:
		return false
	}
}

func validCarrier(carrier Carrier) bool {
	return carrier == CarrierNone || carrier == CarrierHTTP3WebTransport
}
