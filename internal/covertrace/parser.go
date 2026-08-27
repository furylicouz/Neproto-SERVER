package covertrace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	MaximumLineBytes       = 16 * 1024
	MaximumRecords         = 500_000
	MaximumTraces          = 4_096
	MaximumRecordsPerTrace = 262_144
	MaximumRelativeTimeUS  = int64(24 * 60 * 60 * 1_000_000)
	MaximumWireBytes       = 65_535
	MaximumAddedDelayUS    = int64(250_000)
)

var ErrInvalidTrace = errors.New("invalid cover trace")

type Direction string

const (
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

type Record struct {
	TraceID        string    `json:"trace_id"`
	Label          string    `json:"label"`
	RelativeTimeUS int64     `json:"relative_time_us"`
	Direction      Direction `json:"direction"`
	WireBytes      int       `json:"wire_bytes"`
	AddedDelayUS   *int64    `json:"added_delay_us,omitempty"`
}

type traceValidation struct {
	label       string
	lastTimeUS  int64
	records     int
	initialized bool
}

func ReadJSONL(reader io.Reader) ([]Record, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidTrace)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4*1024), MaximumLineBytes)
	records := make([]Record, 0, 256)
	validations := make(map[string]traceValidation)
	for line := 1; scanner.Scan(); line++ {
		raw := scanner.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		if len(records) >= MaximumRecords {
			return nil, fmt.Errorf("%w: record limit exceeded", ErrInvalidTrace)
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		var record Record
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrInvalidTrace, line, err)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrInvalidTrace, line, err)
		}
		state, exists := validations[record.TraceID]
		if !exists && len(validations) >= MaximumTraces {
			return nil, fmt.Errorf("%w: trace limit exceeded", ErrInvalidTrace)
		}
		if err := validateRecord(record, state, exists); err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrInvalidTrace, line, err)
		}
		state.label = record.Label
		state.lastTimeUS = record.RelativeTimeUS
		state.records++
		state.initialized = true
		validations[record.TraceID] = state
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan JSONL: %v", ErrInvalidTrace, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidTrace)
	}
	return records, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateRecord(record Record, state traceValidation, exists bool) error {
	if !safeLabel(record.TraceID) || !safeLabel(record.Label) {
		return errors.New("unsafe trace identifier or label")
	}
	if record.Direction != DirectionUp && record.Direction != DirectionDown {
		return errors.New("direction must be up or down")
	}
	if record.RelativeTimeUS < 0 || record.RelativeTimeUS > MaximumRelativeTimeUS {
		return errors.New("relative time outside bounds")
	}
	if record.WireBytes <= 0 || record.WireBytes > MaximumWireBytes {
		return errors.New("wire size outside bounds")
	}
	if record.AddedDelayUS != nil && (*record.AddedDelayUS < 0 || *record.AddedDelayUS > MaximumAddedDelayUS) {
		return errors.New("added delay outside bounds")
	}
	if exists {
		if state.label != record.Label {
			return errors.New("trace label changed")
		}
		if state.initialized && record.RelativeTimeUS < state.lastTimeUS {
			return errors.New("trace time moved backwards")
		}
	}
	if state.records >= MaximumRecordsPerTrace {
		return errors.New("per-trace record limit exceeded")
	}
	return nil
}

func safeLabel(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '-' || current == '_' || current == '.' {
			continue
		}
		return false
	}
	return true
}
