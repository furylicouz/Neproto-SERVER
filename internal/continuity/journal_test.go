package continuity

import (
	"errors"
	"testing"
)

func TestJournalAppendAckAndReplayAcrossRingWrap(t *testing.T) {
	journal, err := NewJournal(8)
	if err != nil {
		t.Fatal(err)
	}
	start, end, err := journal.Append([]byte("abcdef"))
	if err != nil || start != 0 || end != 6 {
		t.Fatalf("first append start=%d end=%d err=%v", start, end, err)
	}
	if err := journal.Ack(4); err != nil {
		t.Fatalf("ack: %v", err)
	}
	start, end, err = journal.Append([]byte("ghijkl"))
	if err != nil || start != 6 || end != 12 {
		t.Fatalf("wrapped append start=%d end=%d err=%v", start, end, err)
	}

	destination := make([]byte, 8)
	n, next, err := journal.Replay(4, destination)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := string(destination[:n]); got != "efghijkl" || next != 12 {
		t.Fatalf("replay got=%q next=%d", got, next)
	}
	state := journal.State()
	if state.BaseOffset != 4 || state.EndOffset != 12 || state.BufferedBytes != 8 || state.CapacityBytes != 8 {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestJournalBudgetFailureDoesNotMutateState(t *testing.T) {
	journal, err := NewJournal(4)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Append([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	before := journal.State()
	if _, _, err := journal.Append([]byte("de")); !errors.Is(err, ErrReplayBudget) {
		t.Fatalf("append error=%v", err)
	}
	if after := journal.State(); after != before {
		t.Fatalf("failed append mutated state: before=%+v after=%+v", before, after)
	}
}

func TestJournalAckIsCumulativeAndIdempotent(t *testing.T) {
	journal, err := NewJournal(16)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Append([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Ack(3); err != nil {
		t.Fatal(err)
	}
	if err := journal.Ack(3); err != nil {
		t.Fatalf("duplicate cumulative ack: %v", err)
	}
	if err := journal.Ack(2); !errors.Is(err, ErrReplayOffset) {
		t.Fatalf("regressing ack error=%v", err)
	}
	if err := journal.Ack(9); !errors.Is(err, ErrReplayOffset) {
		t.Fatalf("future ack error=%v", err)
	}
	if state := journal.State(); state.BaseOffset != 3 || state.EndOffset != 8 || state.BufferedBytes != 5 {
		t.Fatalf("unexpected state after ACK errors: %+v", state)
	}
}

func TestJournalReplayValidatesOffsetsWithoutAllocation(t *testing.T) {
	journal, err := NewJournal(64)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Append([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Ack(2); err != nil {
		t.Fatal(err)
	}
	destination := make([]byte, 3)

	if _, _, err := journal.Replay(1, destination); !errors.Is(err, ErrReplayOffset) {
		t.Fatalf("retired offset error=%v", err)
	}
	if _, _, err := journal.Replay(9, destination); !errors.Is(err, ErrReplayOffset) {
		t.Fatalf("future offset error=%v", err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		n, next, replayErr := journal.Replay(2, destination)
		if replayErr != nil || n != 3 || next != 5 {
			panic("unexpected replay result")
		}
	})
	if allocations != 0 {
		t.Fatalf("replay allocations=%f", allocations)
	}
}

func TestJournalCloseIsIdempotentAndRejectsFurtherUse(t *testing.T) {
	journal, err := NewJournal(8)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Append([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	journal.Close()
	if state := journal.State(); !state.Closed || state.BufferedBytes != 0 || state.CapacityBytes != 0 {
		t.Fatalf("closed state=%+v", state)
	}
	if _, _, err := journal.Append([]byte("x")); !errors.Is(err, ErrReplayClosed) {
		t.Fatalf("append after close error=%v", err)
	}
	if err := journal.Ack(0); !errors.Is(err, ErrReplayClosed) {
		t.Fatalf("ack after close error=%v", err)
	}
	if _, _, err := journal.Replay(0, make([]byte, 1)); !errors.Is(err, ErrReplayClosed) {
		t.Fatalf("replay after close error=%v", err)
	}
}

func TestNewJournalRejectsInvalidCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, MaxJournalCapacity + 1} {
		if _, err := NewJournal(capacity); !errors.Is(err, ErrReplayConfig) {
			t.Fatalf("capacity=%d error=%v", capacity, err)
		}
	}
}
