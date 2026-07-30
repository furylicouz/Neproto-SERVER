# Implementation Plan: NP/2 Constellation

## Overview

NP/2 Constellation evolves the existing NP/2 v2.2 session into a continuity-first
traffic fabric. A logical VPN session may own multiple short-lived,
independently authenticated carrier leases. Logical flows survive a carrier
failure or network change, while every carrier follows a bounded grammar native
to HTTPS, HTTP/3, or WebRTC. The feature is additive and disabled unless both
peers negotiate the corresponding capabilities.

## Architecture decisions

- Keep the existing NP/2 v2.2 wire behavior as the compatibility baseline.
- Add capabilities and encrypted control envelopes instead of changing the
  existing cell-kind permutation.
- Authenticate every carrier independently before it may attach to a logical
  constellation; attaching never grants more authority than the authenticated
  user already has.
- Use random 128-bit constellation and flow identifiers scoped to a bounded
  lifetime. Never expose destinations, user identifiers, or secrets in metrics.
- Preserve a logical flow with byte offsets, cumulative acknowledgements, and a
  bounded replay journal; never replay bytes beyond the last authenticated ACK.
- Keep carrier grammars declarative and bounded. A manifest is data, not remote
  executable code.
- Use only standard cryptographic primitives. The forward-secret extension uses
  X25519, HKDF-SHA-256, and the existing authenticated transcript.
- Ship every incomplete phase behind negotiated capabilities and safe defaults.

## Dependency graph

```text
Wire contract + capability negotiation
    |
    +-- bounded replay journal
    |       |
    |       +-- logical flow registry
    |               |
    |               +-- cross-carrier resume coordinator
    |
    +-- lease ticket contract
    |       |
    |       +-- client/server constellation registry
    |
    +-- carrier grammar contract
            |
            +-- HTTPS / HTTP3 / WebRTC grammar drivers
                    |
                    +-- flow-aware scheduler

All of the above -> server/desktop/iOS integration -> classifier and rollout gates
```

## Phase 0: Contract and compatibility

### Task 1: Freeze the Constellation contract

**Description:** Record compatibility, trust boundaries, control messages,
limits, and failure semantics before behavior changes.

**Acceptance criteria:**

- [ ] NP/2 v2.2 remains the default when capabilities are absent.
- [ ] No target is dialed before normal NP/2 authentication and lease attach.
- [ ] Every identifier, buffer, lifetime, retry, and carrier count has a bound.

**Verification:** Documentation review against `docs/SPEC.md` and `AGENTS.md`.

**Dependencies:** None.

**Files likely touched:**

- `docs/SPEC.md`
- `docs/NP2-CONSTELLATION-SPEC.md`
- `docs/decisions/ADR-003-constellation-continuity.md`

**Estimated scope:** Medium.

### Task 2: Add the continuity control codec

**Description:** Add a canonical, bounded control envelope transported only
inside authenticated and encrypted NP/2 extension cells.

**Acceptance criteria:**

- [ ] Canonical round trips pass for every message kind.
- [ ] Zero IDs, oversized tokens, non-canonical varints, trailing bytes, and
  invalid type-specific fields are rejected.
- [ ] Legacy peers ignore the optional capability and continue as v2.2.

**Verification:** `go test ./internal/protocol` plus a targeted fuzz smoke.

**Dependencies:** Task 1.

**Files likely touched:**

- `internal/protocol/continuity.go`
- `internal/protocol/continuity_test.go`
- `internal/protocol/continuity_fuzz_test.go`

**Estimated scope:** Medium.

### Task 3: Implement a bounded replay journal

**Description:** Retain only unacknowledged flow bytes and release acknowledged
ranges without unbounded memory or duplicate delivery.

**Acceptance criteria:**

- [ ] Append, cumulative ACK, replay from offset, and close are deterministic.
- [ ] Gaps, regressing ACKs, overflow, and per-flow/session budget exhaustion
  return stable errors.
- [ ] The journal cannot allocate above configured limits.

**Verification:** `go test -race ./internal/continuity` and benchmarks.

**Dependencies:** Task 2.

**Files likely touched:**

- `internal/continuity/journal.go`
- `internal/continuity/journal_test.go`
- `internal/continuity/errors.go`

**Estimated scope:** Medium.

### Checkpoint A: Foundation

- [ ] Protocol, session, and continuity tests pass with race detection.
- [ ] Legacy extension negotiation fixture passes unchanged.
- [ ] Fuzz parser accepts no crash, panic, or unbounded allocation.

## Phase 1: Logical constellation and leases

### Task 4: Add bounded lease tickets

**Description:** Issue short-lived, single-attach tickets after a fully
authenticated NP/2 session. Store only bounded server-side state and rotate a
ticket after successful use.

**Acceptance criteria:**

- [ ] Expired, replayed, wrong-user, or wrong-transcript tickets are rejected.
- [ ] Ticket lookup cannot trigger a target dial.
- [ ] Registry capacity and cleanup time are bounded.

**Verification:** Unit tests with injected clock/randomness and race tests.

**Dependencies:** Tasks 2-3.

**Files likely touched:**

- `internal/continuity/ticket.go`
- `internal/continuity/ticket_test.go`
- `internal/continuity/registry.go`
- `internal/continuity/registry_test.go`

**Estimated scope:** Medium.

### Task 5: Add the logical flow registry

**Description:** Separate a random logical flow ID from the current
session-local stream ID and keep its upstream ownership stable across leases.

**Acceptance criteria:**

- [ ] Flow IDs are random, non-zero, collision-checked, and scoped to one user.
- [ ] Duplicate attach is idempotent; conflicting attach closes only that flow.
- [ ] Per-user and global flow limits remain enforced.

**Verification:** Unit/race tests and an in-memory two-lease integration test.

**Dependencies:** Task 4.

**Files likely touched:**

- `internal/continuity/flow.go`
- `internal/continuity/flow_test.go`
- `internal/continuity/registry.go`
- `internal/continuity/registry_test.go`

**Estimated scope:** Medium.

### Task 6: Add the lease controller

**Description:** Own one primary and a bounded number of warm leases, retire
idle leases, and expose deterministic state transitions to the scheduler.

**Acceptance criteria:**

- [ ] Maximum active, warm, dialing, and draining leases are independently
  bounded.
- [ ] Stop cancels all dialing and closes every lease exactly once.
- [ ] A failed optional lease never disconnects a healthy legacy session.

**Verification:** Deterministic clock tests and `go test -race`.

**Dependencies:** Task 4.

**Files likely touched:**

- `internal/constellation/controller.go`
- `internal/constellation/controller_test.go`
- `internal/constellation/lease.go`

**Estimated scope:** Medium.

### Checkpoint B: Multi-lease control plane

- [ ] Two independently authenticated in-memory carriers attach to one logical
  constellation.
- [ ] Ticket replay and registry exhaustion tests pass.
- [ ] Disabling the capability produces byte-for-byte legacy behavior.

## Phase 2: Cross-carrier flow continuity

### Task 7: Add resumable client flow adapters

**Description:** Wrap a physical NP/2 stream with logical offsets and a bounded
replay journal without changing the public `io.ReadWriteCloser` behavior.

**Acceptance criteria:**

- [ ] Ordered writes receive stable offsets.
- [ ] A physical-stream failure preserves only unacknowledged bytes.
- [ ] Cancellation and close cannot resurrect a flow.

**Verification:** Unit/race tests with injected carrier failures.

**Dependencies:** Tasks 3, 5, and 6.

**Files likely touched:**

- `internal/constellation/stream.go`
- `internal/constellation/stream_test.go`
- `internal/constellation/controller.go`

**Estimated scope:** Medium.

### Task 8: Preserve server upstream ownership

**Description:** Keep one validated upstream connection attached to a logical
flow while physical NP/2 streams are replaced.

**Acceptance criteria:**

- [ ] Resume never redials the destination.
- [ ] A flow cannot migrate across users or constellation IDs.
- [ ] Upstream close/timeout cleans all journals and leases exactly once.

**Verification:** Localhost proxy integration tests and race detection.

**Dependencies:** Tasks 5 and 7.

**Files likely touched:**

- `internal/proxy/continuity.go`
- `internal/proxy/continuity_test.go`
- `internal/continuity/flow.go`

**Estimated scope:** Medium.

### Task 9: Implement migration orchestration

**Description:** Resume active logical flows on a replacement authenticated
lease using offsets, cumulative ACKs, and bounded retry rules.

**Acceptance criteria:**

- [ ] Forced HTTPS-to-HTTP/3 and HTTP/3-to-HTTPS migration preserves a test
  download without duplicate or missing bytes.
- [ ] Simultaneous migration resolves to one accepted lease epoch.
- [ ] Failed migration falls back or fails with a stable sanitized category.

**Verification:** Real localhost carrier E2E, race tests, and fault injection.

**Dependencies:** Tasks 7-8.

**Files likely touched:**

- `internal/constellation/migration.go`
- `internal/constellation/migration_test.go`
- `tests/e2e/constellation_migration_test.go`

**Estimated scope:** Medium.

### Checkpoint C: Continuity data plane

- [ ] A long transfer survives forced carrier closure.
- [ ] Bytes are exact under loss, duplicate control messages, and cancellation.
- [ ] Legacy single-carrier throughput regression is below 2% when disabled.

## Phase 3: Carrier-native grammar and scheduling

### Task 10: Define bounded carrier grammar contracts

**Description:** Describe lease lifetime, burst, idle, concurrency, and request
shape as validated carrier-specific data rather than a universal cell schedule.

**Acceptance criteria:**

- [ ] Every parameter has a minimum, maximum, and safe default.
- [ ] Invalid or unknown mandatory grammar fields fail before carrier creation.
- [ ] Manifests cannot execute code or override security policy.

**Verification:** Parser tests, fuzzing, and compatibility fixtures.

**Dependencies:** Task 2.

**Files likely touched:**

- `internal/grammar/manifest.go`
- `internal/grammar/manifest_test.go`
- `internal/grammar/validate.go`

**Estimated scope:** Medium.

### Task 11: Implement HTTPS grammar driver

**Description:** Replace a fixed permanent WebSocket pattern with bounded HTTPS
lease lifecycles while preserving valid HTTP/TLS behavior.

**Acceptance criteria:**

- [ ] All bytes remain inside valid HTTPS/WebSocket semantics.
- [ ] Lease rotation never interrupts logical flows.
- [ ] Connection count and lifetime remain inside configured budgets.

**Verification:** Local Caddy integration test and PCAP state validation.

**Dependencies:** Tasks 6, 9, and 10.

**Files likely touched:**

- `internal/grammar/https.go`
- `internal/grammar/https_test.go`
- `internal/carrier/httpsws/grammar.go`
- `internal/carrier/httpsws/grammar_test.go`

**Estimated scope:** Medium.

### Task 12: Implement HTTP/3 and WebRTC grammar drivers

**Description:** Map reliable streams, datagrams, idle behavior, and congestion
feedback to their actual carrier semantics instead of copying HTTPS timing.

**Acceptance criteria:**

- [ ] HTTP/3 and WebRTC have independent bounded state machines.
- [ ] Datagram traffic never silently falls back to an invalid reliable mode.
- [ ] UDP blocking triggers authenticated HTTPS fallback.

**Verification:** Carrier integration tests and UDP-blocked E2E.

**Dependencies:** Tasks 6, 9, and 10.

**Files likely touched:** Split into separate HTTP/3 and WebRTC increments of at
most five files each.

**Estimated scope:** Two medium increments.

### Task 13: Add the flow-aware scheduler

**Description:** Select a lease using observable local flow properties, queue
pressure, RTT, loss, and throughput without parsing application payloads.

**Acceptance criteria:**

- [ ] Interactive, bulk, and datagram flows use deterministic bounded rules.
- [ ] The scheduler cannot starve a flow or exceed a carrier budget.
- [ ] Hysteresis prevents rapid carrier oscillation.

**Verification:** Table-driven tests, benchmarks, and recorded-network replay.

**Dependencies:** Tasks 10-12.

**Files likely touched:**

- `internal/constellation/scheduler.go`
- `internal/constellation/scheduler_test.go`
- `internal/constellation/flow_class.go`

**Estimated scope:** Medium.

### Checkpoint D: Adaptive traffic fabric

- [ ] Carrier-specific state machines pass conformance tests.
- [ ] Automatic routing improves or matches the best healthy carrier in the
  recorded Wi-Fi/LTE matrix.
- [ ] No payload or destination is logged or inspected by the scheduler.

## Phase 4: Inner forward secrecy

### Task 14: Add X25519 transcript binding

**Description:** Mix an ephemeral X25519 shared secret into authenticated NP/2
cell-key derivation while retaining PSK authorization and downgrade protection.

**Acceptance criteria:**

- [ ] Both ephemeral keys and the negotiated capability are transcript-bound.
- [ ] Missing mandatory support fails before application cells.
- [ ] Known-answer, reflection, replay, and downgrade tests pass.

**Verification:** Protocol tests, fuzzing, race tests, and independent vectors.

**Dependencies:** Checkpoint C.

**Files likely touched:** Split into contract/vector and integration increments,
each no larger than five files.

**Estimated scope:** Two medium increments.

### Task 15: Add epoch key ratcheting

**Description:** Rotate directional cell keys after bounded byte/time epochs and
destroy retired keys after authenticated acknowledgement.

**Acceptance criteria:**

- [ ] Epoch transitions are ordered and idempotent.
- [ ] Old keys are erased after the bounded overlap window.
- [ ] Migration cannot reuse a nonce/key pair.

**Verification:** Long-run, boundary, concurrent migration, and nonce-uniqueness
tests.

**Dependencies:** Task 14.

**Files likely touched:** Split into protocol and session increments.

**Estimated scope:** Two medium increments.

## Phase 5: Product integration

### Task 16: Integrate server and desktop client

**Description:** Add safe opt-in configuration, metrics, probe output, and
operator controls without exposing secrets or per-target information.

**Acceptance criteria:**

- [ ] Feature defaults off until both peers negotiate it.
- [ ] `probe` reports capability and lease counts without identifiers.
- [ ] `np` can enable, disable, and roll back Constellation safely.

**Verification:** Config tests, command tests, bundle smoke, and canary deploy.

**Dependencies:** Checkpoints C-D and Task 15.

**Files likely touched:** Split into server, client, and manager increments.

**Estimated scope:** Three medium increments.

### Task 17: Integrate iOS Packet Tunnel

**Description:** Preserve one-button operation, fast stop, network migration,
background lifecycle, and sanitized diagnostics on a physical iPhone.

**Acceptance criteria:**

- [ ] No new required user field is added.
- [ ] Wi-Fi/LTE migration preserves active downloads and page loads.
- [ ] Stop closes all leases promptly and leaves no stuck VPN configuration.

**Verification:** Mac build, signed physical-device install, LTE/Wi-Fi switch,
background/foreground, stop, and media smoke.

**Dependencies:** Task 16.

**Files likely touched:** Split into mobile binding, Packet Tunnel, and UI
diagnostics increments.

**Estimated scope:** Three medium increments.

## Phase 6: Evidence and rollout

### Task 18: Add DPI/PCAP evaluation gates

**Description:** Compare NP/2 carriers against matched browser HTTPS, HTTP/3,
and WebRTC controls using only metadata available to an on-path observer.

**Acceptance criteria:**

- [ ] Reproducible captures contain no secrets or user payloads in artifacts.
- [ ] The report includes packet-size, direction, burst, lifetime, TLS/QUIC
  fingerprint, ROC-AUC, and false-positive measurements.
- [ ] A regression threshold blocks camouflage claims, not normal builds.

**Verification:** Reproducible local lab command and reviewed report.

**Dependencies:** Checkpoint D.

**Files likely touched:** Tooling and reports in isolated increments.

**Estimated scope:** Multiple medium increments.

### Task 19: Staged production rollout

**Description:** Deploy server-first with all new capabilities disabled, enable
for a canary credential, validate rollback, then expand gradually.

**Acceptance criteria:**

- [ ] Legacy v2.2 client remains functional after server deployment.
- [ ] Canary passes HTTPS, HTTP/3, WebRTC, migration, stop, and throughput gates.
- [ ] Rollback restores the previous binary/config without credential loss.

**Verification:** Signed artifacts, live canary evidence, metrics, and rollback
rehearsal.

**Dependencies:** Tasks 16-18.

**Files likely touched:** Deployment bundle, operations docs, and release report.

**Estimated scope:** Multiple medium increments.

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Resume duplicates or loses bytes | Critical | Offset invariants, cumulative ACKs, fault-injection E2E |
| Replay journal exhausts memory | Critical | Per-flow, per-user, and global hard limits |
| Multi-lease pattern becomes a fingerprint | High | Carrier-native lifecycle plus classifier gate |
| New peer breaks legacy negotiation | High | Additive optional capability and golden compatibility fixtures |
| Migration crosses authorization boundary | Critical | Bind tickets to authenticated user and transcript |
| Ratchet creates nonce reuse | Critical | Direction/epoch-bound derivation and uniqueness tests |
| iOS backgrounding kills warm leases | High | One primary lease, bounded warm leases, physical-device lifecycle tests |
| Feature lowers throughput | Medium | Disabled baseline, benchmarks, and staged canary |

## Explicitly not doing

- Arbitrary bytes outside valid TLS, HTTP, QUIC, DTLS, SCTP, or WebSocket.
- A fixed packet-size imitation of a named game or application.
- Custom encryption algorithms.
- Cross-user or cross-server flow migration in the first production release.
- Unbounded buffering to make every TCP flow resumable indefinitely.
- Claims of GFW/TSPU invisibility without matched external measurements.
