package continuity

import "testing"

func BenchmarkJournalAppendAck(b *testing.B) {
	payload := make([]byte, 32*1024)
	journal, err := NewJournal(len(payload))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, end, appendErr := journal.Append(payload)
		if appendErr != nil {
			b.Fatal(appendErr)
		}
		if ackErr := journal.Ack(end); ackErr != nil {
			b.Fatal(ackErr)
		}
	}
}

func BenchmarkJournalReplay(b *testing.B) {
	payload := make([]byte, 64*1024)
	journal, err := NewJournal(len(payload))
	if err != nil {
		b.Fatal(err)
	}
	if _, _, err := journal.Append(payload); err != nil {
		b.Fatal(err)
	}
	destination := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n, next, replayErr := journal.Replay(0, destination)
		if replayErr != nil || n != len(destination) || next != uint64(len(destination)) {
			b.Fatalf("replay n=%d next=%d err=%v", n, next, replayErr)
		}
	}
}
