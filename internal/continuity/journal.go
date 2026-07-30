package continuity

import (
	"sync"

	"neproto.local/chameleon/internal/protocol"
)

const MaxJournalCapacity = 64 * 1024 * 1024

type JournalState struct {
	BaseOffset    uint64
	EndOffset     uint64
	BufferedBytes int
	CapacityBytes int
	Closed        bool
}

// Journal is a bounded circular replay buffer for one direction of one logical
// flow. Offsets are cumulative and bytes below BaseOffset have been
// authenticated by the peer and securely retired.
type Journal struct {
	mu sync.Mutex

	buffer []byte
	head   int
	size   int
	base   uint64
	end    uint64
	closed bool
}

func NewJournal(capacity int) (*Journal, error) {
	if capacity <= 0 || capacity > MaxJournalCapacity {
		return nil, ErrReplayConfig
	}
	return &Journal{buffer: make([]byte, capacity)}, nil
}

func (j *Journal) Append(payload []byte) (uint64, uint64, error) {
	if j == nil {
		return 0, 0, ErrReplayConfig
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return 0, 0, ErrReplayClosed
	}
	if len(payload) > len(j.buffer)-j.size {
		return 0, 0, ErrReplayBudget
	}
	if uint64(len(payload)) > protocol.MaxSequence-j.end {
		return 0, 0, ErrReplayOverflow
	}
	start := j.end
	if len(payload) == 0 {
		return start, start, nil
	}
	tail := (j.head + j.size) % len(j.buffer)
	first := min(len(payload), len(j.buffer)-tail)
	copy(j.buffer[tail:tail+first], payload[:first])
	copy(j.buffer[:len(payload)-first], payload[first:])
	j.size += len(payload)
	j.end += uint64(len(payload))
	return start, j.end, nil
}

// Ack cumulatively retires every byte below offset. Repeating the current
// acknowledgement is idempotent; regressing or future acknowledgements fail.
func (j *Journal) Ack(offset uint64) error {
	if j == nil {
		return ErrReplayConfig
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrReplayClosed
	}
	if offset < j.base || offset > j.end {
		return ErrReplayOffset
	}
	retire := int(offset - j.base)
	if retire == 0 {
		return nil
	}
	j.clearLocked(j.head, retire)
	j.head = (j.head + retire) % len(j.buffer)
	j.size -= retire
	j.base = offset
	if j.size == 0 {
		j.head = 0
	}
	return nil
}

// Replay copies retained bytes beginning at offset into destination without
// allocating. The returned next offset is suitable for the next call.
func (j *Journal) Replay(offset uint64, destination []byte) (int, uint64, error) {
	if j == nil {
		return 0, 0, ErrReplayConfig
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return 0, 0, ErrReplayClosed
	}
	if offset < j.base || offset > j.end {
		return 0, 0, ErrReplayOffset
	}
	available := int(j.end - offset)
	length := min(len(destination), available)
	if length == 0 {
		return 0, offset, nil
	}
	start := (j.head + int(offset-j.base)) % len(j.buffer)
	first := min(length, len(j.buffer)-start)
	copy(destination[:first], j.buffer[start:start+first])
	copy(destination[first:length], j.buffer[:length-first])
	return length, offset + uint64(length), nil
}

func (j *Journal) State() JournalState {
	if j == nil {
		return JournalState{Closed: true}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return JournalState{
		BaseOffset: j.base, EndOffset: j.end,
		BufferedBytes: j.size, CapacityBytes: len(j.buffer), Closed: j.closed,
	}
}

func (j *Journal) Close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	clear(j.buffer)
	j.buffer = nil
	j.head = 0
	j.size = 0
	j.closed = true
}

func (j *Journal) clearLocked(start, length int) {
	first := min(length, len(j.buffer)-start)
	clear(j.buffer[start : start+first])
	clear(j.buffer[:length-first])
}
