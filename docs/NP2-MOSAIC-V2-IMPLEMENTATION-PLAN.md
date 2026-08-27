# Implementation Plan: NP/2 Mosaic v2.3 Polymorphic Scheduling

## Overview

Extend the existing negotiated Mosaic v2.2 scheduler with bounded per-session
profile families and a reproducible metadata-trace evaluator. The change stays
inside the cover layer, preserves the NP/2 wire format, and remains disabled
when `CapabilityMosaicCover` is not negotiated.

## Architecture Decisions

- Keep `CapabilityMosaicCover`; no new negotiation bit is required because the
  receiver does not decode scheduler state.
- Derive sender-local variants from the existing directional cover seed with
  domain-separated HMAC-SHA-256.
- Select from bounded static families instead of generating arbitrary packet
  sizes or delays.
- Delay burst starts, never every bulk cell.
- Make dummy scheduling probabilistic and budgeted, never periodic without
  earned credit.
- Treat classifier results as regression evidence, not an invisibility claim.
- Keep the feature safe for partial rollout and independently revertible.

## Assumptions

- The primary target is Windows and iOS through the existing shared Go core.
- Performance mode must retain the existing zero-delay `stream` path.
- No client process will be launched on this Windows development machine.
- Physical iPhone and production-network evidence are release gates, not local
  implementation evidence.
- No dependency, cryptographic primitive, carrier preface, or third-party
  protocol impersonation is added in this phase.

## Tasks

### Task 1: Contract and baseline

**Acceptance criteria:** Mosaic v2.3 behavior, threat model, budgets,
compatibility, diagnostics, and evaluator input are normative; current focused
tests and planner benchmarks are recorded.

**Verification:** documentation review; `go test ./internal/cover`; existing
planner benchmarks.

### Task 2: Per-session profile families

**Acceptance criteria:** web, realtime, and stream definitions are derived once
per session; same seed is deterministic; a seed corpus covers multiple
variants; all values remain within class limits; hot-path updates allocate
nothing beyond the existing planner behavior.

**Verification:** RED/GREEN unit tests, deterministic replay, benchmark.

### Task 3: Burst and gap morphing

**Acceptance criteria:** only burst starts receive bounded delay; selected
delay distributions differ across variants; dummy requests are gated and use
bounded gap delays; stream remains zero-delay/dummy-free; global credit remains
authoritative across transitions.

**Verification:** RED/GREEN state-machine tests, long mixed-load budget test,
transport integration test.

### Checkpoint A

- Focused cover tests pass.
- Existing fixed-profile and Mosaic v2.2 tests remain green.
- Benchmarks show no throughput-path regression outside the agreed budget.

### Task 4: Aggregate diagnostics

**Acceptance criteria:** cover stats expose variant, bursts, selected/suppressed
dummy requests, cumulative delay, and maximum delay; no payload, destination,
seed, or exact packet trace enters production logs.

**Verification:** stats unit tests, client result serialization tests, iOS host
bridge compilation tests where available.

### Task 5: Metadata trace evaluator

**Acceptance criteria:** bounded JSONL input, trace-level dataset split,
deterministic feature extraction, baseline classifier metrics, diversity score,
and malformed-input errors; no external ML dependency.

**Verification:** parser/fuzz tests, known-dataset metrics test, command build.

### Task 6: Session integration and compatibility

**Acceptance criteria:** both-negotiated sessions use the polymorphic scheduler;
legacy or unselected peers stay fixed; quiet remains unchanged; no wire or
authentication downgrade is introduced.

**Verification:** authenticated-session negotiation matrix and carrier
integration tests.

### Checkpoint B

- `gofmt` clean.
- `go test ./...` passes.
- `go test -race ./...` passes in the approved Go environment.
- `go vet ./...` and `go build ./cmd/...` pass.
- Before/after cover benchmarks and evaluator report are recorded.

### Task 7: Device and network release gate

**Acceptance criteria:** signed iPhone build connects; LTE and Wi-Fi tests record
throughput, p50/p95 latency, reconnect behavior, actual overhead, and
metadata-only captures against the fixed Mosaic baseline.

**Verification:** physical device and production/staging evidence. This task is
not satisfied by source, local tests, simulator builds, or Docker alone.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Randomness forms a new signature | High | Bounded overlapping component families and classifier regression |
| Padding reduces media speed | High | Zero-delay stream class and explicit overhead budgets |
| Variant selection leaks seed | High | Expose only a small local identifier; never log derivation material |
| Partial rollout breaks peers | High | No decoder state or new capability; existing cell semantics only |
| Synthetic classifier overstates privacy | Medium | Treat physical captures and independent review as separate gates |
| Dummy scheduling grows unbounded | High | Existing credit ceiling, bounded queue, bounded input and counters |

## Not Doing

- Claiming that Mosaic is invisible or impossible to classify.
- Forging third-party IP addresses, certificates, or proprietary protocols.
- Adding arbitrary bytes outside a standards-compliant carrier.
- Replacing NP/2 authentication or ChaCha20-Poly1305.
- Enabling automatic multi-carrier fallback during the current HTTP/3-isolation
  phase.
