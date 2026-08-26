# NP/2 cross-platform client implementation plan

Status: **approved for implementation on 2026-08-26**

Date: 2026-08-26

Specification: `docs/NP2-CROSS-PLATFORM-CLIENT-SPEC.md`

---

## 1. Outcome

Deliver a candidate replacement client for Windows and iOS with a shared
Flutter application layer, typed Pigeon Host API, instance-based Go ClientCore
and native privileged tunnel hosts.

The initial candidate uses exactly one carrier: HTTP/3 WebTransport over
QUIC/TLS 1.3 on UDP/443. WebRTC and HTTPS/WebSocket connectors remain absent
from the constructed runtime.

This plan does not authorize deployment, server changes, stable-client cutover
or execution of the Windows client on the development machine.

---

## 2. Dependency graph

```text
approved specification
        |
        v
toolchain lock + build safety gates
        |
        v
Host API v1 source + generated Dart/Swift/C++
        |                         |
        |                         +-------------------+
        v                                             v
instance ClientCore + strict HTTP/3 tests       Flutter fake host
        |                                             |
        +--------------------+------------------------+
                             |
               +-------------+-------------+
               |                           |
               v                           v
      Windows service adapter      iOS native host adapter
               |                           |
               v                           v
      Windows external-PC gate     physical-iPhone gate
               |                           |
               +-------------+-------------+
                             |
                             v
               migration + candidate packaging
                             |
                             v
                    publication review gate
```

Contract generation and ClientCore behavior are foundations. Windows and iOS
platform integration may proceed independently only after those foundations
pass their checkpoint. Candidate publication waits for both real-device gates.

---

## 3. Architecture decisions carried into implementation

| Decision | Implementation consequence |
| --- | --- |
| Flutter owns presentation only | No routes, credentials, packet handling or reconnect timers in Dart. |
| Pigeon 28.0.0 owns Host API generation | Dart, Swift and C++ outputs are generated together and checked for drift. |
| Go ClientCore is instance-based | Native hosts own independent context, lifetime and injected dependencies. |
| Strict HTTP/3 is a construction rule | Alternate connectors are `nil`/absent and test spies must observe zero calls. |
| Native stores remain authoritative | Migration starts read-only and never exports credentials to Flutter. |
| Native status is authoritative | Flutter uses monotonic sequences and refreshes after resume/restart. |
| Windows runtime is remote-only | Local automation may compile but cannot run app, service, setup, Wintun or route smoke. |
| iOS runtime uses the real extension | Simulator-only success is not an acceptance result. |

---

## 4. Phase 0 — repository and toolchain safety

### Purpose

Create a reproducible build boundary before product code is introduced.

### Work

- Record exact Flutter 3.44.7 and Pigeon 28.0.0 constraints.
- Bootstrap Flutter on Windows for analysis/test/build only.
- Bootstrap the same Flutter release on `mac-89` for iOS builds.
- Create a dedicated NeProto checkout at
  `/Users/intimnyjprysik/Downloads/Neproto` on `mac-89`; no NeProto checkout
  exists there at plan time.
- Add a repository command that regenerates all Pigeon outputs from one source.
- Add a build guard that refuses local Windows runtime/smoke commands in the
  unified-client workflow.
- Extend CI without replacing the existing WPF/SwiftUI client jobs.
- Keep dependency caches outside source-controlled directories.

### Verification checkpoint A

- Flutter version output is exactly the pinned release on both build hosts.
- A minimal generated Pigeon API compiles for Dart, Swift and Windows C++.
- Re-running generation produces no diff.
- Flutter analyze/unit tests can run without starting native VPN components.
- Windows release compilation produces an artifact but no process launch.

### Stop condition

If Flutter 3.44.7, Pigeon 28.0.0, Xcode 26.6 or the available Windows compiler
cannot generate and compile the three contract targets together, resolve the
toolchain incompatibility before any ClientCore or UI implementation.

---

## 5. Phase 1 — Host API v1 contract

### Purpose

Freeze one testable Flutter/native contract before platform adapters diverge.

### Work

- Define version negotiation and capabilities.
- Define redacted profile summaries and import/select/remove requests.
- Define connect/disconnect commands with idempotent operation IDs.
- Define tunnel state, status sequence, traffic counters and current carrier.
- Define stable error code/stage/retryability data.
- Define bounded diagnostics snapshots and host-to-Flutter status callbacks.
- Generate Dart, Swift, C++ and mock/test bindings.
- Add contract fixtures shared across host-language tests where practical.

### Verification checkpoint B

- Unsupported major versions fail closed.
- Every first-slice method has a round-trip serialization test.
- Unknown enum values become `unknown` or a stable compatibility error.
- Oversized and malformed inputs fail before a host side effect.
- Contract fixtures contain no root secret or full onboarding value in results.
- Generated code is reproducible and carries the same Pigeon version.

### Boundary

This phase does not connect to a server, Wintun, Network Extension or native
profile store.

---

## 6. Phase 2 — instance-based ClientCore

### Purpose

Extract the existing reusable Go lifecycle/data-plane behavior without
changing the NP/2 wire or shipping carrier policy.

### Work

- Introduce an instance-owned ClientCore interface and implementation.
- Move lifecycle state, counters, contexts and goroutine ownership out of the
  package-global mobile controller.
- Preserve the gomobile package-level facade as a temporary compatibility
  adapter for the legacy iOS client.
- Introduce a stable internal status/error mapper used by both native hosts.
- Make strict HTTP/3 connector construction explicit and injectable.
- Retain current HTTP/3, NP/2 session, packet stack and metrics packages.
- Add bounded same-carrier reconnect orchestration and deterministic tests.

### TDD order

1. Failing test: two core instances cannot share mutable session state.
2. Failing test: `http3-only` constructs one HTTP/3 connector.
3. Failing test: HTTP/3 failure invokes zero WebRTC/HTTPS dialers.
4. Failing test: cancellation closes all owned goroutines/queues.
5. Failing test: reconnect respects six attempts and the 30-second deadline.
6. Failing test: internal failures map to stable redacted errors.
7. Implement only enough behavior to satisfy each test.

### Verification checkpoint C

```powershell
go test -race ./internal/clientcore ./internal/clienthost ./mobile/np2mobile
go vet ./internal/clientcore ./internal/clienthost ./mobile/np2mobile
go test ./internal/carrier/http3wt ./internal/app
```

- Existing mobile controller tests remain green.
- The NP/2 wire fixtures do not change.
- Test dialer counters prove alternate-carrier calls are exactly zero.
- Race and leak-oriented tests show no post-close owned goroutines.

### Boundary

Do not remove adaptive carriers from the server or legacy clients. Strictness is
in the new candidate's ClientCore configuration/construction path.

---

## 7. Phase 3 — shared Flutter vertical slice with fake host

### Purpose

Complete the user journey independently of privileged OS integration.

### Work

- Create the unified Flutter app and plugin packages.
- Implement `ClientHost` abstraction and generated-Pigeon adapter.
- Implement one `ClientSessionController` with immutable view state.
- Build Home, Profiles and Diagnostics screens.
- Support onboarding-value import, selection and removal through the fake host.
- Display only HTTP/3 WebTransport; do not add a selector.
- Handle asynchronous status callbacks and resume/restart refresh.
- Add Russian localization, semantics and keyboard/Dynamic Type behavior.

### Vertical-slice scenarios

1. Empty client -> import -> select -> connect -> connected -> disconnect.
2. Existing profile -> connect -> HTTP/3-stage failure -> diagnostics.
3. Connected host -> UI restart -> authoritative connected state restored.
4. Reconnecting host -> stale callback arrives -> newer sequence wins.

### Verification checkpoint D

```powershell
Set-Location clients/unified/app
dart format --output=none --set-exit-if-changed .
flutter analyze
flutter test
flutter build windows --release
```

- Widget and application tests use only a fake Host API.
- The Windows artifact is not launched locally.
- Golden/semantic tests cover disconnected, connecting, connected,
  reconnecting and failed states.
- No Dart persistence contains profile or credential fields.

---

## 8. Phase 4 — Windows native host vertical slice

### Purpose

Connect the shared UI to the existing privileged Windows implementation while
preserving route and credential safety.

### Work

- Implement the Pigeon-generated Windows C++ host adapter.
- Add a bounded RAII named-pipe client to the plugin.
- Version the internal service IPC and add structured errors.
- Review and tighten named-pipe locality, security descriptor and client
  identity behavior.
- Adapt existing service profile/list/import/select/remove operations.
- Adapt connect/disconnect/status/diagnostics to instance ClientCore.
- Preserve DPAPI records, selected profile, route journal and safe-uplink order.
- Update packaging to include the Flutter runner while retaining rollback data.

### Verification checkpoint E1 — local static/build only

```powershell
go test ./internal/windowsclient ./internal/clientcore ./internal/clienthost
go vet ./internal/windowsclient ./internal/clientcore ./internal/clienthost
go build ./cmd/neproto-windows-service ./cmd/neproto-windows-setup
Set-Location clients/unified/app
flutter test
flutter build windows --release
```

No produced `.exe` is executed on the development machine.

### Verification checkpoint E2 — separate Windows test PC

- Install candidate and preserve an existing profile.
- Confirm unelevated UI can access only the intended local service methods.
- Connect and prove the service reports `http3_webtransport`.
- Exercise YouTube, Telegram and Instagram media scenarios.
- Block UDP/443 and verify a stage-specific HTTP/3 failure with zero fallback.
- Restart UI and verify the service remains authoritative.
- Disconnect/uninstall and verify owned routes, DNS and Wintun state are clean.
- Collect sanitized app/service logs and server-side carrier evidence.

### Stop condition

Do not begin Windows publication packaging if profile migration, pipe security,
route rollback or zero-fallback evidence fails.

---

## 9. Phase 5 — iOS native host vertical slice

### Purpose

Connect the same Flutter flow to Apple's native VPN lifecycle without embedding
Flutter in the Packet Tunnel extension.

### Work

- Embed the Flutter runner and Swift Pigeon host in the iOS container target.
- Retain the separate Swift `NEPacketTunnelProvider` target.
- Adapt current profile/UserDefaults and Keychain stores behind Host API.
- Preserve Keychain persistent references and suppressed cluster profile IDs.
- Adapt `NETunnelProviderManager` connect/disconnect/status handling.
- Add bounded provider-message diagnostics and stable error mapping.
- Bind the extension to instance ClientCore through the gomobile framework.
- Set `reasserting`, reconnect only HTTP/3, and terminate cleanly on exhaustion.
- Complete `startTunnel` only after network settings and packet processing work.

### Verification checkpoint F1 — Mac builds

```bash
cd /Users/intimnyjprysik/Downloads/Neproto
clients/ios/Scripts/build-frameworks.sh
cd clients/unified/app
flutter analyze
flutter test
flutter build ios --release --no-codesign
```

- Swift host/extension and generated Pigeon code compile under Xcode 26.6.
- App and extension entitlements remain distinct and valid.
- Tests prove existing Keychain material never crosses into Dart.

### Verification checkpoint F2 — physical iPhone

- Install the signed candidate on `Furylicouz`.
- Confirm existing profiles appear without reimport.
- Connect and verify only HTTP/3 WebTransport on device and server logs.
- Exercise YouTube, Telegram and Instagram media scenarios.
- Perform Wi-Fi reconnect and Wi-Fi/cellular transitions.
- Verify same-carrier reconnect or a stable terminal failure; no fallback.
- Restart the container UI while connected and verify status resynchronization.
- Disconnect and confirm Network Extension teardown.

### Stop condition

Simulator builds, Swift unit tests or a successful container launch do not
replace physical Packet Tunnel evidence.

---

## 10. Phase 6 — migration, hardening and candidate release

### Purpose

Turn the two proven vertical slices into a rollback-safe candidate without
changing server or wire behavior.

### Work

- Run idempotent migration tests against sanitized legacy-store fixtures.
- Add secret-redaction, fuzz/input-bound and lifecycle stress suites.
- Add unified-client jobs to CI while retaining legacy client verification.
- Produce Windows and iOS candidate version metadata from repository `VERSION`.
- Document installation, rollback and evidence collection.
- Compare device results against the specification acceptance matrix.
- Publish only after explicit release approval.

### Verification checkpoint G

- Full Go race, vet, build and vulnerability gates pass.
- Flutter format, analyze, unit/widget/integration and release builds pass.
- Swift/native tests and signed iPhone build pass.
- Windows external-PC and iPhone evidence are attached to the candidate.
- Alternate-carrier dial count is zero in tests and observed server traces.
- Rollback preserves profiles and credentials on both platforms.
- No server deployment is required.

---

## 11. Checkpoint policy

| Checkpoint | Required before |
| --- | --- |
| A — toolchains and Pigeon generation | Host API implementation |
| B — Host API contract | ClientCore/platform adapter integration |
| C — ClientCore strict HTTP/3 | Native tunnel integration |
| D — fake-host Flutter journey | Platform UI wiring |
| E2 — Windows external runtime | Windows candidate packaging |
| F2 — physical iPhone runtime | iOS candidate packaging |
| G — complete evidence | Publication request |

Failure at a checkpoint returns work to the owning phase. It does not authorize
a fallback carrier, TLS bypass, local Windows execution or unrelated rewrite.

---

## 12. Parallel and sequential work

### Must be sequential

- Specification -> plan -> task breakdown.
- Toolchain proof -> Host API generation.
- Host API -> native adapters.
- ClientCore strict-policy tests -> platform connection integration.
- Platform build -> corresponding real-device runtime test.
- Both platform gates -> publication.

### May be parallel after checkpoint C

- Flutter screen implementation against the fake host.
- Windows native adapter implementation.
- iOS native adapter implementation.
- Migration fixture preparation and redaction tests.

Parallel work must not edit the canonical Pigeon contract independently. Host
API changes are serialized and regenerated for every language together.

---

## 13. Risks and mitigations

| Risk | Severity | Mitigation |
| --- | --- | --- |
| Generated Swift/C++ API incompatibility | 🟠 High | Prove Pigeon on both toolchains in phase 0; pin one version. |
| ClientCore refactor changes wire behavior | 🔴 Critical | Preserve fixtures/facade; diff protocol vectors; focused race tests. |
| Hidden fallback survives existing connector wiring | 🔴 Critical | Construction-level absence plus zero-call spies and server evidence. |
| Windows pipe grants excessive local control | 🟠 High | ACL/locality/token review and external-PC negative tests. |
| Existing profile migration loses credentials | 🔴 Critical | Read-first adapters, sanitized fixtures, idempotency and rollback. |
| iOS container success masks extension failure | 🟠 High | Physical Packet Tunnel gate is mandatory. |
| Local Windows build script starts smoke binaries | 🔴 Critical | Unified workflow excludes legacy runtime smoke; explicit build guard. |
| UI state diverges after process restart | 🟡 Medium | Native authority, monotonic sequence and resume refresh tests. |
| HTTP/3 media problem remains | 🟠 High | Stage timing plus real-device/server evidence before feature expansion. |

---

## 14. Deliverables

1. Approved specification and implementation plan.
2. Approved discrete task breakdown with per-task acceptance commands.
3. Reproducible Flutter/Pigeon toolchain configuration.
4. Host API v1 generated bindings and compatibility tests.
5. Instance-based Go ClientCore and legacy gomobile adapter.
6. Shared Flutter Home, Profiles and Diagnostics flow.
7. Windows native adapter/service candidate and external-PC evidence.
8. iOS native adapter/Packet Tunnel candidate and physical-device evidence.
9. Migration, rollback and security verification report.
10. Unpublished candidate artifacts ready for explicit publication approval.

---

## 15. Plan approval gate

Approval of this plan authorizes creation of the discrete Phase 3 task
breakdown. It does not yet authorize implementation. The task breakdown will
identify exact files, acceptance criteria and verification commands, and is the
final review gate before code changes begin.
