# NP/2 Mosaic Implementation Report

Date: 2026-07-18

Scope: repository implementation and local Windows verification; no production
deployment or physical-device rollout.

━━━━━━━━━━━━━━━━━━━━
📊 IMPLEMENTATION REPORT
━━━━━━━━━━━━━━━━━━━━

## Executive Summary

NP/2 now negotiates `CapabilityMosaicCover` additively after authentication and
cell AEAD activation. When both peers support it, each sender adapts its own
cover schedule among `web`, `realtime`, and a zero-delay `stream` fast path.
Old peers and `quiet` profiles keep their fixed behavior.

The implementation adds no dependency, handshake field, mandatory TLV, cell
kind, or unauthenticated wire bytes. All existing Go packages and end-to-end
tests pass, including the complete Linux race suite in an isolated Go 1.26.5
Docker container.

## Findings

| Area | Finding | Impact | Severity | Evidence |
|---|---|---|---|---|
| Compatibility | Mosaic is bit 4 of the existing authenticated capability bitset | Old peers select a subset and continue with fixed cover | 🟢 Low | Protocol and authenticated-session legacy tests |
| Classification | Bounded 500 ms counters plus two-window hysteresis select web/realtime/stream | Avoids payload inspection and short-burst oscillation | 🟢 Low | Deterministic unit tests repeated 100 times |
| Throughput path | Stream disables delay and dummy scheduling and caps padding at 256 bytes | Removes the fixed-profile delay/dummy hot-path penalty during bulk transfer | 🟢 Low | Stream transition and dummy suppression tests |
| Privacy | Diagnostics expose only cover mode, class, and transition count | Operators can verify negotiation without logging targets or secrets | 🟢 Low | `neproto-client probe` output test |
| Race verification | Windows lacks CGO, so the same repository was mounted read-only into Linux Go 1.26.5 | Complete race evidence is available without changing production | 🟢 Low | `docker run ... golang:1.26.5 go test -race ./...` |
| Carrier scope | HTTP/3 and a complete optional game front are not part of this slice | Traffic remains on the existing real HTTPS/WebRTC carriers | 🟡 Medium | Normative spec and Phase 7 roadmap |

## Risks

- Classification uses aggregate outbound cells because one authenticated NP/2
  session multiplexes many logical flows. Mixed simultaneous workloads can
  select a compromise class rather than a per-flow carrier.
- The classifier changes size/timing behavior; it does not erase observable
  traffic volume, destination IP, connection lifetime, or carrier metadata.
- Local benchmarks prove scheduler cost, not end-to-end VPN throughput on LTE,
  Wi-Fi, or the production VPS.

## Verification

| Check | Result |
|---|---|
| `go test ./...` | ✅ All packages and E2E passed |
| `go test -coverprofile=coverage.out ./...` | ✅ Passed; `internal/cover` 84.8% |
| Mosaic tests repeated 100 times | ✅ Passed |
| Extension-envelope fuzz, 10 s | ✅ 4,671,158 executions; no failure |
| `go vet ./...` | ✅ Passed |
| `go build ./cmd/...` | ✅ Passed |
| `govulncheck v1.6.0 ./...` | ✅ 0 called/imported vulnerabilities; one unreachable required-module advisory |
| Mosaic classifier benchmark | ✅ 59.89–61.26 ns/op, 0 B/op, 0 allocs/op |
| Complete Mosaic planner benchmark | ✅ 539.6–564.1 ns/op, 136 B/op, 2 allocs/op |
| Linux `go test -race ./...` in `golang:1.26.5` | ✅ Final post-change run: all packages and E2E passed in 55.3 s |

## Recommendations

1. Add a repeatable traffic-shape harness and capture actual class transitions,
   throughput, p50/p95 latency, overhead, CPU, and allocations.
2. Deploy server-first to staging, verify a legacy client and a Mosaic client,
   then test the physical iPhone on LTE and Wi-Fi.
3. Implement Browser-H3/Stream-H3 only as real HTTP/3, and implement an optional
   game front only with a complete compatible protocol state machine.

## Action Items

| Priority | Task | Status |
|---:|---|---|
| P0 | Linux/CGO race run | ✅ Done |
| P0 | Staging server/client compatibility test | ⏳ Pending |
| P1 | Physical iPhone throughput/latency/capture validation | ⏳ Pending |
| P1 | HTTP/3 carrier contract and implementation | 📋 Roadmap |
| P2 | Optional complete game-front research and active-probe suite | 📋 Roadmap |
