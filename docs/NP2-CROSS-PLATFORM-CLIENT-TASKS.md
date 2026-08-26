# NP/2 cross-platform client task breakdown

Status: **approved by the 2026-08-26 continuous-execution directive**

Specification: `docs/NP2-CROSS-PLATFORM-CLIENT-SPEC.md`

Plan: `docs/NP2-CROSS-PLATFORM-CLIENT-PLAN.md`

Rules for every task:

- write the focused failing test first;
- implement the smallest passing change;
- run the listed verification once after the last relevant change;
- keep HTTP/3 WebTransport as the only constructed carrier;
- never execute a Windows client/service/setup/Wintun workflow locally;
- commit each green task as an atomic save point.

---

## Phase A — contract and core foundations

### Task A1 — Pin and guard the unified toolchain

**Status:** ✅ Completed in `f82de21` (Windows build-only toolchain guard;
native compiler gates remain at their platform checkpoints).

**Acceptance criteria:**

- Flutter 3.44.7 and Pigeon 28.0.0 are pinned.
- One command regenerates Dart, Swift and C++ bindings.
- The Windows verification entrypoint has build-only semantics and rejects
  runtime/smoke flags locally.

**Verification:**

```powershell
flutter --version
dart run pigeon --version
pwsh clients/unified/tool/verify-windows.ps1 -BuildOnly
```

**Files:** `clients/unified/tool/*`, `clients/unified/plugin/pubspec.yaml`,
`.github/workflows/ci.yml`.

### Task A2 — Define Host API version and capabilities

**Status:** ✅ Completed on the implementation branch.

**Acceptance criteria:**

- Pigeon source defines API version `1.0` and platform capabilities.
- Unsupported major versions fail closed.
- Generated files are reproducible.

**Verification:** `flutter test test/host/host_api_version_test.dart` and
generation drift check.

**Files:** `clients/unified/plugin/pigeons/client_host_api.dart`, generated
Dart/Swift/C++ outputs, one contract test.

### Task A3 — Define profiles and lifecycle contract

**Status:** ✅ Completed on the implementation branch.

**Acceptance criteria:**

- Profile import/list/select/remove and connect/disconnect/status are typed.
- Mutations carry bounded operation IDs.
- Results cannot contain secret-bearing fields.

**Verification:** `flutter test test/host/host_api_contract_test.dart`.

**Files:** Pigeon source, generated outputs, contract test.

### Task A4 — Define diagnostics and callback contract

**Acceptance criteria:**

- Stable error code/stage/retryability and bounded diagnostics are typed.
- Status callbacks include monotonic sequence.
- Unknown values do not become success states.

**Verification:** `flutter test test/host/host_api_diagnostics_test.dart`.

**Files:** Pigeon source, generated outputs, contract test.

### Task A5 — Add stable Go host status/errors

**Acceptance criteria:**

- Go types represent Host API states, stages and redacted errors.
- Validation enforces all first-slice input bounds.
- Raw internal errors never cross the mapper.

**Verification:** `go test -race ./internal/clienthost`.

**Files:** `internal/clienthost/*.go` and tests.

### Task A6 — Introduce instance-owned ClientCore lifecycle

**Acceptance criteria:**

- Two instances do not share session state or cancellation.
- Close is idempotent and releases owned work.
- The legacy gomobile facade remains compatible.

**Verification:**
`go test -race ./internal/clientcore ./mobile/np2mobile`.

**Files:** `internal/clientcore/*.go`, tests, focused gomobile adapter changes.

### Task A7 — Enforce strict HTTP/3 construction

**Acceptance criteria:**

- New candidate configuration constructs one HTTP/3 connector.
- WebRTC and HTTPS connector call counts remain zero on success and failure.
- Error stage distinguishes DNS, QUIC/TLS, WebTransport and NP/2 auth.

**Verification:**
`go test -race ./internal/clientcore -run 'HTTP3|Carrier|Stage'`.

**Files:** ClientCore policy/connector files and focused tests.

### Task A8 — Add bounded same-carrier reconnect

**Acceptance criteria:**

- Reconnect probes current session, then uses HTTP/3 only.
- Six-attempt and 30-second total bounds are deterministic in tests.
- Exhaustion yields a stable error and closes owned state.

**Verification:**
`go test -race ./internal/clientcore -run 'Reconnect|NetworkChanged'`.

**Files:** ClientCore reconnect files and tests.

---

## Phase B — shared Flutter journey

### Task B1 — Bootstrap the unified app and fake host

**Acceptance criteria:**

- Windows/iOS Flutter targets compile from one app package.
- `ClientHost` has a deterministic fake implementation.
- App startup negotiates API version and refreshes native status.

**Verification:** `flutter analyze` and
`flutter test test/application/client_session_controller_test.dart`.

**Files:** app bootstrap, Host abstraction/fake, controller and test.

### Task B2 — Implement Home lifecycle slice

**Acceptance criteria:**

- Home renders all six cross-platform tunnel states.
- Connect/disconnect commands are idempotently guarded in UI state.
- HTTP/3 is displayed without a transport selector.

**Verification:** `flutter test test/screens/home_screen_test.dart`.

**Files:** Home screen, controller/state additions, widget test.

### Task B3 — Implement profile lifecycle slice

**Acceptance criteria:**

- Existing profiles list/select/remove through Host API.
- Onboarding value is passed once and is never persisted in Dart.
- Empty and invalid imports render stable validation errors.

**Verification:** `flutter test test/screens/profiles_screen_test.dart`.

**Files:** Profiles screen, controller/state additions, widget test.

### Task B4 — Implement diagnostics lifecycle slice

**Acceptance criteria:**

- Diagnostics show stable code/stage and bounded redacted events.
- Stale status sequence cannot overwrite newer state.
- Resume/restart restores authoritative host status.

**Verification:** `flutter test test/screens/diagnostics_screen_test.dart`.

**Files:** Diagnostics screen, sequence handling, widget/controller tests.

### Checkpoint B

```powershell
Set-Location clients/unified/app
dart format --output=none --set-exit-if-changed .
flutter analyze
flutter test
flutter build windows --release
```

The produced executable is not launched locally.

---

## Phase C — Windows vertical slice

### Task C1 — Implement bounded Windows plugin IPC

**Acceptance criteria:**

- C++ Pigeon host uses RAII named-pipe I/O with deadlines and 256 KiB bounds.
- API version, malformed-frame and host-unavailable errors are stable.
- Unit tests do not require a running NeProto service.

**Verification:** Windows plugin unit tests and release compilation only.

**Files:** Windows plugin host, pipe adapter, tests, CMake configuration.

### Task C2 — Version and harden service IPC

**Acceptance criteria:**

- Service implements Host API-compatible version negotiation/errors.
- Pipe rejects remote/anonymous access and preserves the 16-client bound.
- No method can return credential material.

**Verification:** `go test -race ./internal/windowsclient`.

**Files:** Windows IPC/API files and focused tests.

### Task C3 — Adapt Windows profile lifecycle

**Acceptance criteria:**

- Existing `%ProgramData%\NeProto` profiles and DPAPI records remain readable.
- Import/select/remove preserve current transactional behavior.
- Migration fixtures are idempotent and secret-redacted.

**Verification:** focused Windows store/profile tests.

**Files:** Windows store/profile adapter and tests.

### Task C4 — Adapt Windows strict tunnel lifecycle

**Acceptance criteria:**

- Service uses ClientCore HTTP/3-only path.
- Endpoint exclusions precede dial; Wintun/routes follow authentication.
- Failures roll back operation-owned state.

**Verification:** Windows backend/route tests with fakes; compile only locally.

**Files:** Windows controller/backend adapters and tests.

### Task C5 — Package and verify on the external Windows PC

**Acceptance criteria:**

- Candidate installer preserves legacy profiles and rollback data.
- External-PC media/UDP-block/UI-restart/cleanup matrix passes.
- Client and server evidence show zero alternate-carrier attempts.

**Verification:** signed evidence report from the separate Windows test PC.

**Files:** packaging scripts, candidate workflow, verification report.

---

## Phase D — iOS vertical slice

### Task D1 — Create iOS Flutter container host

**Acceptance criteria:**

- Swift Pigeon host owns `NETunnelProviderManager` operations.
- Packet Tunnel remains a separate Flutter-free extension.
- Native status events map to the shared sequence/state model.

**Verification:** Swift unit tests and unsigned Mac build.

**Files:** iOS plugin host, runner integration, tests, Xcode configuration.

### Task D2 — Adapt iOS profile and Keychain lifecycle

**Acceptance criteria:**

- Existing profiles, selected state and Keychain references remain readable.
- Root secrets never enter Dart or UserDefaults.
- Import/removal is transactional and idempotent.

**Verification:** Swift profile/Keychain migration tests.

**Files:** Swift host adapters and tests.

### Task D3 — Adapt Packet Tunnel to ClientCore

**Acceptance criteria:**

- Extension establishes only HTTP/3 WebTransport.
- `startTunnel` completes only after settings and packet path succeed.
- Network transitions use bounded same-carrier reconnect.

**Verification:** gomobile/Swift builds plus deterministic Go lifecycle tests.

**Files:** PacketTunnel provider, gomobile facade, ClientCore adapter tests.

### Task D4 — Verify on physical iPhone

**Acceptance criteria:**

- Existing profile, media, network-transition and UI-restart matrix passes.
- Device/server evidence proves HTTP/3-only and clean teardown.
- No simulator result is substituted for the physical Packet Tunnel gate.

**Verification:** signed device verification report for `Furylicouz`.

**Files:** verification report and candidate metadata only.

---

## Phase E — release hardening

### Task E1 — Complete CI and security gates

**Acceptance criteria:**

- Go race/vet/build/vulnerability and Flutter format/analyze/test/build pass.
- Generated bindings have a no-diff check.
- Redaction, bounds, migration and lifecycle stress suites pass.

**Verification:** all new and existing CI jobs green.

**Files:** CI workflow and focused verification scripts.

### Task E2 — Produce unpublished candidate

**Acceptance criteria:**

- Windows and iOS artifacts share the repository version.
- Checksums, migration and rollback notes are complete.
- Real-device evidence is linked; no stable deployment is triggered.

**Verification:** artifact inspection and checksum verification.

**Files:** build/release scripts and release notes.

### Task E3 — Publish after final evidence review

**Acceptance criteria:**

- Candidate commits are pushed to the approved Git remote/branch.
- Publication contains no secrets or local build output.
- Stable rollout remains a separately authorized action.

**Verification:** remote commit/tag/artifact hashes match local evidence.

**Files:** Git metadata and release manifest only.
