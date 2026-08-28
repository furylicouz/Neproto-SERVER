# NP/2 Pulse v1

Status: candidate specification for performance and physical-device testing.

Pulse is a sender-local, low-overhead traffic-shaping mode for the authenticated
NP/2 data plane. It does not impersonate a third-party service and does not
change the TLS, WebSocket, authentication, cell, or carrier wire formats. Its
purpose is to make repeated NP/2 sessions avoid one fixed size/timing trace
while preserving the throughput of the direct data path.

The mode name is `pulse`. An endpoint running `pulse` accepts peers running
`off`, `pulse`, or the legacy negotiated `mosaic` mode because receivers already
ignore authenticated NP/2 padding and `DUMMY` cells. Pulse does not advertise
`CapabilityMosaicCover` and does not depend on synchronized classifier state.

## Security boundary

Pulse operates after NP/2 authentication and before the directional cell AEAD.
The schedule seed and padding seed are independently derived from the
authenticated session key material. Consequently, reconnecting produces a new
profile family, padding contents, padding decisions, and burst delays even when
the application trace is repeated.

Pulse is a traffic-analysis mitigation, not an anonymity claim. It does not
hide the server IP address, hosting ASN, connection duration, total byte count,
or correlation visible to a sufficiently broad observer. No implementation or
UI may claim that classification is impossible without a reproducible trace
corpus and an evaluated classifier.

## Normative performance envelope

| Property | Pulse v1 limit |
|---|---:|
| Total padding plus dummy overhead | at most 5% of authenticated real wire bytes |
| Per-session cover credit | at most 65,535 bytes |
| Added delay on the first cell of a burst | at most 2 ms |
| Added delay on following cells in the burst | 0 ms |
| Bulk/stream per-cell delay | 0 ms |
| Padding added to one cell | at most 512 bytes |
| Background dummy cells | none in v1 |
| Periodic constant-rate traffic | prohibited |

The configured `max_cover_overhead_percent` is additionally capped at 5 while
`cover_mode=pulse`. A value of zero disables all Pulse padding and dummy bytes
but retains the bounded burst-start timing schedule for controlled experiments.

## Session-polymorphic size scheduling

Each authenticated direction derives a Pulse family from its schedule seed.
The family selects:

- one of several monotonically increasing target-size palettes;
- a padding-selection gate;
- a maximum per-cell spend below the 512-byte hard ceiling;
- one of the bounded delay distributions;
- a non-secret diagnostic variant identifier.

The target palettes are protocol-internal distributions, not copies of another
protocol or named service. A selected real cell may be padded toward one of the
next palette targets, but it can spend only credit already earned from real
traffic. Non-selected cells accumulate bounded credit. This prevents a fixed
`payload + 5%` relationship and permits the same application trace to map to
multiple external size sequences across sessions.

The scheduler must be deterministic for one seed and trace so it can be tested,
while two independently derived session seeds must not produce an identical
decision sequence for the complete conformance corpus.

## Burst scheduling and fast path

A burst starts with the first real cell or after at least 50 ms without a real
cell. Only that first cell may receive a session-derived delay in the inclusive
range 0..2 ms. All immediately following cells use the zero-delay path. The
scheduler must not sleep per packet and must not introduce an application-rate
ceiling for media or bulk transfers.

Pulse v1 does not emit background dummy cells. Idle-gap dummy insertion remains
reserved for a later version because a dummy must be cancelled safely when new
real traffic arrives; emitting delayed dummies after a burst has resumed would
add contention and a recognizable artifact. The zero-dummy rule is deliberate,
not an unbounded future default.

## Budgets and failure behavior

Padding credit is earned as `real_wire_bytes * effective_percent` and spent in
hundredths of a byte, using saturating arithmetic. Credit, cell size, padding,
queues, counters, and delays remain bounded by the existing cover engine
limits. Budget exhaustion produces an unpadded real cell; it never blocks real
traffic. Invalid configuration fails closed before a session is started.

The receiver performs no Pulse-specific state transition. Unknown cover mode
configuration values are rejected locally and are never sent on the wire.

## Required observability

Diagnostics expose only bounded aggregates: mode, variant ID, real bytes,
padding bytes, burst count, total planned delay, and maximum planned delay.
They must not expose session seeds, padding bytes, keys, destinations, or a
per-packet trace.

## Candidate acceptance gates

Pulse may become a production default only after all of the following pass:

1. deterministic unit tests and the race detector;
2. measured overhead no greater than 5% on web, interactive, and bulk corpora;
3. maximum planned delay no greater than 2 ms and zero delay after burst start;
4. no material regression in the cover planning benchmark;
5. unsigned iOS build plus a signed physical-iPhone A/B test against `off`;
6. a captured, sanitized trace corpus showing session-to-session diversity;
7. a laboratory classifier evaluation against the direct `off` baseline.

Build and unit-test evidence alone do not satisfy the physical throughput or
traffic-analysis gates.

## Research basis

Pulse combines conservative, implementable properties supported by prior work:
bounded connection padding, zero-delay/front-loaded padding, and randomized
many-to-many trace selection. The cited systems report materially different
overheads and threat models; their results are motivation, not performance
claims for NP/2.

- Tor connection-level padding specification:
  <https://spec.torproject.org/padding-spec/connection-level-padding.html>
- FRONT/GLUE, USENIX Security 2020:
  <https://www.usenix.org/conference/usenixsecurity20/presentation/gong>
- Chameleon many-to-many traffic morphing preprint:
  <https://arxiv.org/abs/2608.20160>
