━━━━━━━━━━━━━━━━━━━━
📊 NP/2 MOSAIC v2.3 IMPLEMENTATION REPORT
━━━━━━━━━━━━━━━━━━━━

Date: 2026-08-27  
Branch: `codex/mosaic-v2-polymorphic`

## 📌 Executive Summary

Mosaic v2.3 polymorphic scheduling is implemented in the shared Go core for
Windows and iOS. The implementation stays wire-neutral: it uses the existing
`CapabilityMosaicCover`, authenticated NP/2 padding, and `DUMMY` cells. It adds
no carrier preface, decoder state, cryptographic primitive, or third-party
protocol impersonation.

The source, full test suite, Linux race suite, vet, command builds,
vulnerability scan, and planner benchmarks pass. The Windows client was not
launched on this development machine.

The signed iOS build and physical iPhone LTE/Wi-Fi throughput comparison remain
release gates. No claim of DPI invisibility is made from synthetic tests.

## 🔍 Findings

| Area | Finding | Status | Evidence |
|---|---|---|---|
| Session diversity | Directional HMAC-derived bounded profile families select overlapping size, dummy, delay, gate, and look-ahead components | ✅ Done | Determinism, bounds, and 256-seed corpus tests |
| Burst timing | Only burst starts after 50 ms idle receive bounded distribution-shaped delay | ✅ Done | Front/tail distribution and consecutive-cell tests |
| Dummy traffic | Session gate removes deterministic one-real/one-request behavior; dummy gaps remain credit-bounded | ✅ Done | Gate, gap range, exhaustion, and global overhead tests |
| Stream path | Stream class retains zero added real-cell delay and no dummy cells | ✅ Preserved | Mosaic stream fast-path tests |
| Diagnostics | Variant, bursts, dummy decisions, and cumulative/max delay are aggregate-only | ✅ Done | Engine, desktop probe, mobile core, and iOS bridge tests/source |
| Evaluator | Bounded metadata-only JSONL parser, trace-level split, classifier, histograms, delays, bursts, and diversity score | ✅ Done | Unit tests, fuzz corpus seed, CLI build |
| Compatibility | Both-negotiated peers activate variants; v2.2, v2.1, unselected, and quiet paths remain fixed | ✅ Done | Authenticated session negotiation matrix |
| Wire/security | Existing capability and NP/2 cells are reused; secrets, payloads, destinations, RNG state, and exact packet traces are excluded from production diagnostics | ✅ Preserved | Spec, stats allow-list test, report leak test |

## Verification Matrix

| Gate | Result |
|---|---|
| `go test ./... -count=1` | ✅ Passed, including E2E |
| `go test -race ./... -count=1` | ✅ Passed in `golang:1.26.5-bookworm` Docker |
| `go vet ./...` | ✅ Passed |
| `go build ./cmd/...` | ✅ Passed |
| `govulncheck ./...` | ✅ 0 reachable/package vulnerabilities |
| Parser fuzz corpus seed | ✅ Passed |
| Timed fuzz worker | ⚠️ Not run: system C: had only ~139 MB free |
| iOS compile-only bridge check | ⚠️ Not run: SSH to the Mac closed before command execution |
| Windows client launch | ⛔ Intentionally not run on this machine |
| Physical iPhone LTE/Wi-Fi A/B | ⏳ Pending release gate |

`govulncheck` reports `GO-2026-5932` at module level for the unmaintained
`golang.org/x/crypto/openpgp` package, but that package is not imported or
reachable from this code. The scan reports zero affected symbols and packages.

## Performance Readout

Windows amd64, Intel Xeon Silver 4214R, five benchmark samples:

| Benchmark | Before | After | Allocations |
|---|---:|---:|---:|
| Mosaic planner | 558–574 ns/op | 562–591 ns/op | 136 B/op, 2 allocs/op unchanged |
| Mosaic classifier | ~64–65 ns/op | 59.7–62.0 ns/op | 0 B/op, 0 allocs/op |
| Fixed interactive planner | — | 490–508 ns/op | 136 B/op, 2 allocs/op |
| Transport web burst | — | 777–788 ns/op | 136 B/op, 2 allocs/op |

The final planner range overlaps the recorded baseline and introduces no new
hot-path allocation. Physical network throughput is not inferable from these
CPU microbenchmarks.

## Delivered Commits

| Commit | Scope |
|---|---|
| `44e121b` | Normative specification and implementation plan |
| `7d7bd33` | Per-session profile-family derivation |
| `7cd354e` | Burst/gap morphing and session-gated dummies |
| `158be29` | Aggregate diagnostics for core, desktop, mobile, and iOS |
| `9e6b048` | Metadata trace evaluator and `neproto-coverlab` CLI |
| `43ee7e4` | Authenticated negotiation/legacy compatibility matrix |

## ⚠️ Risks

| Area | Risk | Impact | Severity | Mitigation |
|---|---|---|---|---|
| Device throughput | Padding/dummy policy may still reduce LTE download speed on a physical iPhone | User-visible media slowdown | 🟠 High | Signed device A/B with actual overhead and class diagnostics |
| Classifiability | Synthetic features can understate a carrier or operator classifier | False confidence | 🟠 High | Use approved metadata-only captures from real LTE/Wi-Fi sessions |
| Observable metadata | Server IP, QUIC/TLS properties, connection lifetime, and aggregate volume remain visible | Traffic can still be classified or blocked | 🟠 High | Honest threat model; carrier work must remain standards-compliant |
| iOS bridge | New gomobile symbols have not been compiled by Xcode in this run | Possible build-time integration issue | 🟡 Medium | Rebuild framework and run compile-only Xcode gate on Mac |
| Disk pressure | C: lacks space for timed fuzzing and large local toolchain output | Verification instability | 🟡 Medium | Keep build/temp caches on D: and free C: before fuzz campaigns |

## ✅ Recommendations

1. Rebuild the gomobile framework on the Mac and compile the iOS app and
   PacketTunnel extension without installing or launching them first.
2. Run controlled iPhone A/B tests: fixed Mosaic v2.2 versus polymorphic v2.3
   on LTE and Wi-Fi, with identical endpoint, object, run count, and time window.
3. Record throughput p50/p95, latency p50/p95, reconnects, traffic class,
   padding/dummy overhead, bursts, and added-delay aggregates.
4. Collect only approved metadata JSONL and evaluate it with:

   ```text
   neproto-coverlab evaluate --input trace.jsonl --json report.json --markdown report.md
   ```

5. Accept or tune profile families only from device/network evidence. Do not
   claim invisibility from classifier score alone.

## 🧭 Action Items

| Priority | Task | Status |
|---|---|---|
| P0 | Mac gomobile rebuild and Xcode compile-only verification | ⏳ Pending |
| P0 | Physical iPhone LTE/Wi-Fi throughput and stability A/B | ⏳ Pending |
| P1 | Generate approved fixed-vs-v2.3 metadata traces and evaluator report | ⏳ Pending |
| P1 | Review actual overhead and tune only if stream/media performance regresses | ⏳ Pending |
| P2 | Free C: space and run a 30-second parser fuzz campaign | ⏳ Pending |

