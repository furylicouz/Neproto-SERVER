# Implementation Plan: Neproto Chameleon NP/2

## Overview

Implement NP/2 as small, testable vertical slices. The riskiest mechanisms—authentication transcript, cell codec, cover budget, and real carriers—are proven before VPS deployment. Every behavioral task follows RED -> GREEN -> REFACTOR and ends in a working commit.

## Architecture Decisions

The current cluster, signed catalogue, SSH enrolment and policy-routing work is
tracked in `NP2-CLUSTER-ROUTING-PLAN.md`.

- Public wire behavior belongs to a valid WebRTC or HTTPS carrier.
- NP/2 logic is carrier-independent and cannot depend on Pion/WebSocket types.
- Server challenge replaces timestamp-only first-packet authentication.
- One authenticated session multiplexes bounded logical streams.
- Cover traffic is governed by measured budgets and latency ceilings.
- HTTPS is implemented first as the deterministic reference carrier; WebRTC must pass the same carrier contract suite.
- NP/2 v2.1 encrypts every post-authentication cell with directional AEAD independently of the carrier.
- The iOS Packet Tunnel connects a userspace TCP/IP stack directly to NP/2 logical streams; it does not use a SOCKS listener.

## Phase 6: NP/2 v2.1 Mobile and Cell Encryption

The approved byte-level and lifecycle contract is in `NP2-2.1-IOS-SPEC.md`.

### Task 18: Add mandatory directional cell AEAD

**Acceptance criteria:** independent client-to-server and server-to-client keys; ordered implicit nonces; tampering, replay, reflection, truncation, and feature downgrade close the session; cover padding and dummy cells are encrypted.

**Verification:** focused unit tests, authenticated-session integration tests, race detector, fuzz smoke test.

### Task 19: Add direct iOS TUN-to-NP/2 adapter

**Acceptance criteria:** the duplicated utun FD feeds a pinned userspace IPv4/IPv6 TCP stack; every TCP flow opens a canonical NP/2 target stream; UDP is rejected; no loopback listener is created.

**Verification:** dialer unit tests, gomobile XCFramework build, Swift tests, unsigned and signed device builds.

### Task 20: Upgrade and validate production

**Acceptance criteria:** server and iOS client negotiate mandatory cell AEAD; physical-device web traffic exits through the production NP/2 server; stop/reconnect does not leak a descriptor, stream, or goroutine.

**Verification:** production probe, device logs, external IP request, repeated connect/disconnect cycle.

## Phase 7: NP/2 v2.2 Mosaic Adaptive Cover

The approved negotiation and classifier contract is in
`NP2-MOSAIC-SPEC.md`. This phase improves fixed-profile performance without
claiming or emitting an incomplete game or HTTP/3 protocol.

### Task 21: Negotiate Mosaic additively

**Acceptance criteria:** both peers advertise `CapabilityMosaicCover` in the
existing authenticated capabilities TLV; the feature activates only after a
successful intersection; older peers retain the requested fixed cover profile.

**Verification:** protocol intersection tests and authenticated-session tests
for both negotiated and legacy-peer paths.

### Task 22: Add bounded adaptive classification

**Acceptance criteria:** the allocation-free classifier selects `web`,
`realtime`, or `stream` according to the normative window/rate rules; hysteresis
prevents one-window oscillation; idle and regressed clocks reset safely;
`quiet` is never made adaptive.

**Verification:** RED/GREEN unit tests, race detector, deterministic replay, and
planner benchmarks.

### Task 23: Prove budgets and integrate diagnostics

**Acceptance criteria:** profile transitions preserve the global overhead
credit/accounting; the stream class has zero scheduling delay and no dummy
cells; diagnostics expose only class and aggregate counters.

**Verification:** long mixed-load property test, transport integration test,
full `go test -race`, `go vet`, and command builds.

### Task 24: Add workload-specific carriers

**Acceptance criteria:** `Arena-RTC`, `Browser-H3`, and `Stream-H3` are real
standards-compliant carriers behind the shared contract; any optional game front
implements a complete compatible state machine and is never treated as a
cryptographic boundary.

**Verification:** carrier contract suite, real localhost protocol integration,
active-probe tests, throughput/latency captures, and an external security
review before production enablement.

### Task 25: Add Mosaic v2.3 per-session profile families

**Acceptance criteria:** each directional session deterministically selects a
bounded overlapping combination of size, delay, and dummy policies; every
combination preserves class and global budgets; no decoder or wire state is
added.

**Verification:** derivation corpus tests, deterministic replay, allocation and
planner benchmarks.

### Task 26: Add burst/gap morphing and diagnostics

**Acceptance criteria:** only burst starts are delayed, dummy scheduling is
session-gated and budgeted, stream stays zero-delay/dummy-free, and diagnostics
expose aggregate privacy/performance counters without payload or destinations.

**Verification:** state-machine, transport, budget, stats, and client result
tests.

### Task 27: Add the metadata trace evaluator

**Acceptance criteria:** bounded JSONL traces are split by trace, transformed
into deterministic metadata features, and evaluated with reproducible baseline
accuracy, balanced accuracy, and session-diversity metrics without an external
ML dependency.

**Verification:** parser/fuzz tests, known-dataset test, and command build.

The detailed implementation contract and checkpoints are in
`NP2-MOSAIC-V2-IMPLEMENTATION-PLAN.md`.

## Dependency Graph

```text
project rules/module
    |
auth transcript -> cell codec -> cover engine
                         |
                    stream mux
                    /        \
             HTTPS carrier  WebRTC carrier
                    \        /
                  hybrid selector
                         |
                  SOCKS end-to-end
                         |
                 deployment/measurement
```

## Phase 1: Protocol Foundation

### Task 1: Bootstrap a pinned Go module

**Acceptance criteria:**
- `go.mod` pins approved direct dependencies and no application behavior exists yet.
- Client/server commands build and print a development version.
- Project rules document commands, conventions, and boundaries.

**Verification:** `go mod tidy`, `go test ./...`, `go build ./cmd/...`

**Dependencies:** None

**Files:** `go.mod`, `go.sum`, `AGENTS.md`, `cmd/*/main.go`

### Task 2: Authenticate a carrier session

**Acceptance criteria:**
- A valid challenge/response/confirm transcript succeeds and both peers derive identical session material.
- Replay, cross-challenge response, modified feature bits, expired challenge, and wrong secret fail.
- Parsers enforce 512-byte/canonical limits and never log secrets.

**Verification:** focused unit tests, race detector, fuzz smoke test

**Dependencies:** Task 1

**Files:** `internal/protocol/auth.go`, `auth_test.go`, `auth_fuzz_test.go`

### Task 3: Encode session-specific cells

**Acceptance criteria:**
- Session seed deterministically produces a bijective type mapping.
- Canonical cells round-trip; malformed varints, unknown types, over-limit sizes, and trailing bytes fail.
- Stream/sequence invariants are represented in typed values.

**Verification:** unit tests plus decoder fuzz test

**Dependencies:** Task 2

**Files:** `internal/protocol/cell.go`, `cell_test.go`, `cell_fuzz_test.go`

### Task 4: Enforce adaptive cover budgets

**Acceptance criteria:**
- `quiet`, `web`, and `interactive` produce deterministic schedules from a test seed.
- Real cells never exceed the profile latency ceiling.
- Dummy and padding overhead remains within the configured token budget over a test window.

**Verification:** unit/property tests and benchmark baseline

**Dependencies:** Task 3

**Files:** `internal/cover/engine.go`, `profiles.go`, tests

### Checkpoint 1

- Full tests/race/vet pass.
- Protocol spec and implementation agree byte-for-byte.
- No network carrier dependency exists in core packages.

## Phase 2: Reference End-to-End Path

### Task 5: Multiplex logical streams over a message carrier

**Acceptance criteria:**
- Multiple in-memory streams concurrently exchange ordered bytes.
- Flow-control violation, duplicate sequence, reset, and session shutdown are deterministic.
- Queues and goroutines return to baseline after closure.

**Verification:** race-enabled integration tests and goroutine leak assertion

**Dependencies:** Tasks 3-4

**Files:** `internal/session/*` limited to at most five files

### Task 6: Implement HTTPS carrier contract

**Acceptance criteria:**
- Client/server exchange opaque binary messages over real localhost TLS/WebSocket.
- TLS verification is mandatory and WebSocket compression is disabled.
- Invalid route or upgrade does not enter NP/2 authentication.

**Verification:** localhost certificate integration tests

**Dependencies:** Task 1

**Files:** `internal/carrier/carrier.go`, `internal/carrier/https/*`

### Task 7: Complete one SOCKS-to-target path

**Acceptance criteria:**
- Loopback SOCKS5 CONNECT opens an NP/2 logical stream and reaches a local echo/HTTP target.
- Unsafe destinations are blocked by default.
- Closing either side cancels the complete stream without leaks.

**Verification:** process-level local E2E test

**Dependencies:** Tasks 5-6

**Files:** `internal/socks5/*`, `internal/proxy/*`, `tests/e2e/*`

### Checkpoint 2

- A unique NP/2 authenticated multiplexed protocol works end-to-end over HTTPS.
- Cover overhead and latency are reported for each profile.

## Phase 3: WebRTC and Hybrid Selection

### Task 8: Implement bounded HTTPS signaling

**Acceptance criteria:**
- Valid bounded SDP offer receives an answer; malformed/oversized requests receive a generic decoy-compatible failure.
- Outstanding peer connections and gathering deadlines are capped.
- Signaling does not dial proxy targets.

**Verification:** HTTP integration and abuse-limit tests

**Dependencies:** Task 1

**Files:** `internal/carrier/webrtc/signaling.go`, tests

### Task 9: Implement WebRTC carrier contract

**Acceptance criteria:**
- Pion DataChannel passes the same opaque-message contract suite as HTTPS.
- ICE/DTLS/SCTP teardown releases sockets and goroutines.
- DataChannel limits align with NP/2 cell limits.

**Verification:** real localhost Pion integration tests

**Dependencies:** Task 8

**Files:** `internal/carrier/webrtc/*` limited to at most five files

### Task 10: Select and fall back between carriers

**Acceptance criteria:**
- Healthy WebRTC is selected first.
- Forced UDP/WebRTC failure selects HTTPS within the configured deadline.
- No active stream is duplicated or replayed across carriers.

**Verification:** deterministic selector tests and E2E forced-failure test

**Dependencies:** Tasks 6 and 9

**Files:** `internal/carrier/hybrid/*`, tests

### Checkpoint 3

- Both carriers pass one contract suite.
- SOCKS E2E passes over WebRTC, HTTPS, and forced fallback.

## Phase 4: Operations and Evidence

### Task 11: Add strict configuration and CLIs

**Acceptance criteria:**
- `run`, `check`, `version`, and `generate-secret` commands match the spec.
- Unknown fields, unsafe binds, weak/malformed secrets, and invalid budgets fail before listening.
- Example configs contain placeholders only.

**Verification:** CLI/config tests and secret scan

**Dependencies:** Checkpoint 3

**Files:** `internal/config/*`, command mains, `.env.example` if required

### Task 12: Package hardened deployment

**Acceptance criteria:**
- Caddy serves a real decoy and proxies only approved private routes.
- systemd runs NP/2 as a dedicated user with restrictive sandboxing.
- Rollback and secret rotation are documented.

**Verification:** Caddy validation, systemd security review, local install dry run

**Dependencies:** Task 11 and exact domain

**Files:** `deploy/caddy/*`, `deploy/systemd/*`, `docs/OPERATIONS.md`

### Task 13: Deploy without disturbing existing services

**Acceptance criteria:**
- Zabbix/SSH remain available; approved TCP/UDP ports only are exposed.
- Server and decoy restart cleanly after reboot/service restart.
- Production configs contain generated secrets with restrictive permissions.

**Verification:** service status, listener inventory, external port check, rollback rehearsal

**Dependencies:** Task 12 and user approval for VPS mutation

### Task 14: Measure and compare

**Acceptance criteria:**
- Report p50/p95 setup latency, throughput, CPU, memory, goroutines, and actual cover overhead.
- Capture summaries compare NP/2 profiles and one explicitly documented Xray baseline.
- Results state limitations and do not extrapolate to universal DPI resistance.

**Verification:** repeatable benchmark/E2E commands and saved report

**Dependencies:** Task 13

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| WebRTC complexity delays MVP | High | HTTPS reference path first; shared carrier contract |
| Cover engine harms latency | High | Hard latency ceilings and bypass for urgent real cells |
| Dummy bytes exceed budget | Medium | Token accounting tested as an invariant |
| Multiplexer leaks goroutines | High | Bounded queues, cancellation ownership, race/leak tests |
| Active signaling probe differs from decoy | High | Generic bounded endpoint behavior and no proxy side effect before auth |
| VPS memory pressure | Medium | Baseline before optimization, explicit concurrency caps |

## Final Approval Checkpoint

Implementation begins when the user confirms this plan. Domain is only required before Task 12.
