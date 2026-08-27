# NP/2 Mosaic Cover Negotiation and Scheduling

Status: implementation contract for NP/2 v2.2-compatible peers.

Mosaic is an authenticated, adaptive cover scheduler. It changes only the
padding, dummy-cell, and bounded-delay policy applied to already encrypted NP/2
cells. It does not change the NP/2 cell codec, stream semantics, cell AEAD,
carrier authentication, or destination policy.

## Goals

- Reduce the throughput penalty of the fixed `web` and `interactive` cover
  profiles during sustained bulk transfer.
- Preserve bounded, session-keyed cover behavior for web-like and realtime
  traffic.
- Negotiate the behavior additively so NP/2 v2.1/v2.2 peers without Mosaic
  continue to connect with their requested fixed profile.
- Keep every cover byte inside a valid authenticated NP/2 cell and a valid
  standards-compliant outer carrier.

Mosaic is not a promise that traffic is indistinguishable from a named game or
application. Size and timing remain observable to an on-path party even when
TLS, DTLS, and NP/2 cell AEAD hide content.

## Capability Negotiation

`CapabilityMosaicCover` is bit 4 (`1 << 4`) in the existing 64-bit extension
capabilities value carried by `ExtensionTLVCapabilities`.

The capability is offered and accepted only after the base challenge/response
handshake and cell AEAD are active. No new handshake feature bit or mandatory
TLV is introduced.

```text
selected = server_offer & client_request
Mosaic active iff selected contains CapabilityMosaicCover
```

Compatibility rules:

- A peer that does not offer/request the bit continues with the fixed profile
  selected in the base handshake.
- `quiet` never becomes adaptive, even when the capability is selected.
- `web` starts in the `web` class; `interactive` starts in the `realtime`
  class.
- Each sender classifies only its own aggregate outbound cell stream. The
  receiver does not need the sender's class to decode cells.
- A capability negotiation timeout or optional-extension failure leaves the
  fixed profile active. It must not weaken authentication or cell encryption.

## Classes and Cover Policies

| Mosaic class | Cover definition | Maximum real delay | Maximum padding per cell | Dummy cells |
|---|---|---:|---:|---|
| `web` | Existing `web` profile | 12 ms | 8,192 bytes | Budgeted |
| `realtime` | Existing `interactive` profile | 20 ms | 1,024 bytes | Budgeted |
| `stream` | Bulk fast path | 0 ms | 256 bytes | Disabled |

The global configured overhead percentage and absolute credit ceiling apply to
all classes. Switching class does not reset earned credit or accounting.

## Mosaic v2.3 Polymorphic Scheduling

Mosaic v2.3 is a wire-neutral extension of the negotiated
`CapabilityMosaicCover` behavior. It does not add a capability bit, TLV, cell
kind, preface, or decoder state. A v2.3 sender remains interoperable with a
v2.2 receiver because padding and dummy traffic continue to use ordinary,
authenticated NP/2 cells.

The privacy objective is to reduce repeatable size and timing metadata across
independent sessions. It is not to make traffic random or to claim resistance
to a global observer. Destination IP, carrier metadata, aggregate byte counts,
and connection lifetime remain observable.

### Directional profile derivation

Each sender derives one immutable variant for every eligible Mosaic class from
its directional cover seed. Derivation uses HMAC-SHA-256 with domain-separated
labels; it does not introduce a new cryptographic primitive or use payload
contents.

```text
variant_material = HMAC-SHA-256(
    directional_cover_seed,
    "NP2 Mosaic profile variant" || profile_id
)
```

The material independently selects:

- one size-bucket family;
- one dummy-size family;
- one bounded delay distribution;
- one dummy scheduling gate;
- one bucket look-ahead bound.

All component tables are compile-time bounded and versioned with the
implementation. Selection performs no allocation on the per-cell path.
Different session seeds should normally select different component
combinations, while many sessions intentionally share individual components so
uniqueness does not become a one-session fingerprint.

`quiet` has one zero-overhead variant. `stream` variants keep zero real-cell
delay and no dummy cells. No variant may exceed the class limits in the table
above or the global configured overhead budget.

### Burst and gap behavior

- An outbound burst begins after at least 50 ms without a real cell.
- Only the first real cell in a burst may receive a bounded delay. Consecutive
  cells retain the zero-added-delay fast path.
- Delay samples use the selected bounded distribution; uniform random delay is
  not the only distribution shared by every session.
- Dummy scheduling is gated by the selected session variant and available
  credit. A real cell does not deterministically imply a dummy request.
- A scheduled dummy uses the selected bounded gap-delay distribution. It
  remains inside an authenticated `DUMMY` cell and the valid outer carrier.
- A class transition changes to the pre-derived variant for that class without
  resetting credit, counters, or session keys.

### Observable diagnostics

Implementations may expose only aggregate, payload-free diagnostics:

- active class and a small local variant identifier;
- burst count and class-transition count;
- cumulative added-delay microseconds and maximum planned delay;
- dummy requests selected and rejected by the budget/gate;
- real, padding, and dummy byte totals.

The directional seed, random generator state, destinations, payloads, exact
per-packet timestamps, and full traces must never be emitted by production
diagnostics.

### Evaluation contract

Mosaic changes are not accepted from visual inspection of packet captures.
The repository must provide an offline evaluator that accepts metadata-only
JSONL traces with the following bounded fields:

```text
trace_id | label | relative_time_us | direction | wire_bytes | added_delay_us (optional)
```

`direction` is `up` or `down`. Identifiers use bounded safe labels. Relative
time is monotonic within a trace; wire size and optional planned delay are
bounded by the NP/2 cell and Mosaic delay limits.

The evaluator must reject invalid/unbounded input, split training and test data
by trace rather than packet, and report at minimum:

- trace count and label balance;
- size histogram and burst summary;
- bandwidth overhead and added-delay percentiles when supplied;
- deterministic baseline classifier accuracy and balanced accuracy;
- a session-diversity score for repeated identical workloads.

The classifier is a regression instrument, not proof of invisibility. A change
must also preserve the configured overhead and latency budgets and the stream
throughput fast path.

The repository evaluator is invoked as:

```text
neproto-coverlab evaluate --input trace.jsonl --json report.json --markdown report.md
```

Production clients do not write these traces automatically. Captures must be
collected deliberately for an approved lab run and must contain no payload or
destination fields.

## Deterministic Classifier

The classifier uses no payload contents and retains only bounded counters for
the current observation window.

Constants:

```text
observation window       = 500 ms
idle reset               = 3 s
small cell               = wire size <= 1,200 bytes
stream rate threshold    = 1 MiB/s
stream burst threshold   = 256 KiB in <= 1 s
realtime minimum cells   = 10 per completed window
realtime small-cell ratio= >= 80%
realtime maximum rate    = 512 KiB/s
transition confirmation  = 2 completed windows
```

On each real outbound cell:

1. If time regresses, reset only the observation window; retain the current
   class and cover accounting.
2. If the previous observation is older than the idle-reset interval, propose
   `web`.
3. Complete every elapsed 500 ms window and classify it:
   - `stream` when its byte rate is at least 1 MiB/s;
   - otherwise `realtime` when it contains at least 10 cells, at least 80% are
     small, and its byte rate is at most 512 KiB/s;
   - otherwise `web`.
4. Enter `stream` immediately when the current burst reaches 256 KiB within one
   second. Other transitions require the same candidate in two consecutive
   completed windows. This hysteresis prevents oscillation on short web bursts.
5. Plan the current cell with the resulting class definition.

All arithmetic must be overflow-safe. The classifier performs constant work
and allocation-free updates per cell.

## Security and Privacy Boundaries

- Timing decisions and padding bytes remain derived from directional
  session-key material.
- Dummy traffic is encoded as `DUMMY` cells and authenticated by cell AEAD.
- No unauthenticated bytes, fake UDP packets, forged third-party addresses, or
  incomplete proprietary-game messages are emitted.
- Mosaic does not replace TLS/DTLS certificate validation, NP/2 authentication,
  or ChaCha20-Poly1305 cell protection.
- Classification metrics may contain counts and byte totals, never target
  names, packet payloads, secrets, or full client addresses.

## Future Carrier Profiles

`Arena-RTC`, `Browser-H3`, `Stream-H3`, and an optional fully compatible game
front are separate carrier work. This specification does not label HTTPS or
WebRTC bytes as HTTP/3 or game traffic. A future carrier must pass the shared
carrier contract, complete its real protocol state machine, and negotiate an
additive authenticated capability before Mosaic can select it.

## Required Verification

- Capability encode/parse/intersection tests, including an old-peer offer that
  does not contain Mosaic.
- Deterministic classifier tests for web, realtime, stream, idle reset, time
  regression, and transition hysteresis.
- Property test that total padding plus dummy bytes never exceeds the configured
  cover budget across class transitions.
- Authenticated client/server test proving Mosaic activates only when both peers
  negotiate it.
- Race test and allocation benchmark for the per-cell planner.
- Deterministic profile-family derivation tests proving the same seed is stable,
  distinct seeds cover multiple variants, and every derived component remains
  within its class bounds.
- Burst/gap tests proving only burst starts are delayed, dummy requests are not
  deterministic per real cell, and the global overhead budget is preserved.
- Metadata evaluator parser bounds, trace-level split, deterministic metrics,
  and malformed-input tests.
- Before/after planner benchmarks and synthetic repeated-workload diversity
  tests. Physical-device throughput and classifier evidence remain separate
  release gates.

## Design Sources

- [TLS 1.3 Appendix E.3](https://www.rfc-editor.org/rfc/rfc8446.html#appendix-E.3)
  documents that length and timing can reveal information even when record
  contents are protected.
- [WebRTC Data Channels, RFC 8831](https://www.rfc-editor.org/rfc/rfc8831.html)
  defines the standards-compliant realtime carrier used by NP/2.
- [Tor pluggable transports proposal 180](https://spec.torproject.org/proposals/180-pluggable-transport.html)
  motivates keeping the transport boundary replaceable instead of coupling
  NP/2 Core to one disguise.
- [Xray-core Minecraft FinalMask PR 6210](https://github.com/XTLS/Xray-core/pull/6210)
  is an official public example of a complete named-protocol front whose own
  author explicitly keeps cryptographic security in the upper layer. NP/2 does
  not copy its packet layout or treat such a front as encryption.
