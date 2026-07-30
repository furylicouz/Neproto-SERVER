package session

import "sync/atomic"

// Stats is a payload- and destination-free snapshot of one NP/2 multiplexer.
// Counters are monotonic for the lifetime of the Mux, except ActiveStreams.
type Stats struct {
	LocallyOpenedStreams  uint64
	RemotelyOpenedStreams uint64
	ActiveStreams         uint64
	RetiredStreams        uint64
	SentCells             uint64
	ReceivedCells         uint64
	SentCellPayloadBytes  uint64
	ReceivedPayloadBytes  uint64
	WindowUpdatesSent     uint64
	WindowUpdatesReceived uint64
	FlowControlStalls     uint64
	ProtocolErrors        uint64
}

type muxStats struct {
	locallyOpenedStreams  atomic.Uint64
	remotelyOpenedStreams atomic.Uint64
	retiredStreams        atomic.Uint64
	sentCells             atomic.Uint64
	receivedCells         atomic.Uint64
	sentCellPayloadBytes  atomic.Uint64
	receivedPayloadBytes  atomic.Uint64
	windowUpdatesSent     atomic.Uint64
	windowUpdatesReceived atomic.Uint64
	flowControlStalls     atomic.Uint64
	protocolErrors        atomic.Uint64
}

func (m *Mux) Stats() Stats {
	if m == nil {
		return Stats{}
	}
	m.mu.Lock()
	activeStreams := len(m.streams)
	m.mu.Unlock()
	return Stats{
		LocallyOpenedStreams:  m.stats.locallyOpenedStreams.Load(),
		RemotelyOpenedStreams: m.stats.remotelyOpenedStreams.Load(),
		ActiveStreams:         uint64(activeStreams),
		RetiredStreams:        m.stats.retiredStreams.Load(),
		SentCells:             m.stats.sentCells.Load(),
		ReceivedCells:         m.stats.receivedCells.Load(),
		SentCellPayloadBytes:  m.stats.sentCellPayloadBytes.Load(),
		ReceivedPayloadBytes:  m.stats.receivedPayloadBytes.Load(),
		WindowUpdatesSent:     m.stats.windowUpdatesSent.Load(),
		WindowUpdatesReceived: m.stats.windowUpdatesReceived.Load(),
		FlowControlStalls:     m.stats.flowControlStalls.Load(),
		ProtocolErrors:        m.stats.protocolErrors.Load(),
	}
}
