# NP/2 cross-platform client specification

Status: **approved for implementation on 2026-08-26**

Date: 2026-08-26

Owner: NeProto client platform

This document defines a replacement client architecture for Windows and iOS.
It does not change the NP/2 public wire format or the server contract. The
normative wire contract remains `docs/SPEC.md` and
`docs/NP2-2.2-PRODUCTION-SPEC.md`.

The first deliverable in this specification is intentionally limited to one
carrier: HTTP/3 WebTransport over QUIC/TLS 1.3 on UDP/443. It must not dial,
race, promote, or fall back to WebRTC or HTTPS/WebSocket.

---

## 1. Objective

Build one maintainable client product for Windows and iOS with:

- a shared Flutter presentation and application-state layer;
- one reusable Go NP/2 client core;
- native privileged hosts for operating-system VPN integration;
- a versioned, typed boundary between the Flutter UI and native hosts;
- migration from existing profiles without reimporting credentials;
- deterministic diagnostics that identify the failed HTTP/3 connection stage;
- a path to Android and macOS that does not break the first public Host API.

The replacement must improve client maintainability without moving VPN
privileges, secrets, routes, packet processing, or reconnect ownership into
Flutter.

### 1.1 Terminology

| Term | Meaning in this document |
| --- | --- |
| NP/2 | NeProto's authenticated, multiplexed application/session protocol. |
| Carrier | The standards-compliant connection that carries NP/2 records. |
| Initial carrier | HTTP/3 WebTransport over QUIC/TLS 1.3 on UDP/443 only. |
| ClientCore | Shared Go lifecycle, NP/2 session, TUN packet and statistics logic. |
| Native host | Privileged or OS-specific code that owns the VPN lifecycle. |
| Host API | Typed, versioned Flutter-to-native application contract. |

### 1.2 Product boundary

The Flutter process is a control and presentation client. It is not the VPN
engine. Closing or crashing the Flutter window must not corrupt routes or leave
partial tunnel state. The native host remains responsible for cleanup.

---

## 2. Scope

### 2.1 Initial supported platforms

| Platform | Minimum | Architecture | Runtime owner |
| --- | --- | --- | --- |
| Windows | Windows 10 22H2 / Windows 11 | x64 | LocalSystem service |
| iOS | iOS 16 | arm64 physical device | `NEPacketTunnelProvider` extension |

Windows arm64, Android, macOS and Linux desktop are additive future targets and
are not acceptance gates for the initial implementation.

### 2.2 First vertical slice

The first candidate must support only:

1. loading the existing native profile store;
2. importing a validated `np2://import/v1/` or `np2://import/v2/` onboarding
   value;
3. listing, selecting and removing profiles;
4. connecting and disconnecting through strict HTTP/3 WebTransport;
5. reporting tunnel state, carrier, current throughput, total bytes and a
   stable last error;
6. surviving a UI restart while the native tunnel state remains authoritative;
7. bounded same-carrier reconnection after a network transition.

### 2.3 Explicit non-goals for the first vertical slice

- No WebRTC carrier.
- No HTTPS/WebSocket compatibility carrier.
- No carrier racing, promotion or fallback.
- No transport selector in the UI.
- No server, NP/2 cell, authentication or cryptographic wire changes.
- No new routing/catalogue editor UI until the base lifecycle passes on both
  real target devices.
- No secret export, secret display or debug payload capture.
- No simultaneous rewrite of the Windows installer, cluster control plane or
  server deployment.
- No launch of the Windows NeProto app, service, setup program, Wintun adapter
  or route-changing smoke test on the development machine.

The existing WPF and SwiftUI clients remain available during development and
rollback. They are not deleted until the replacement passes the release gates.

---

## 3. Technology stack

| Layer | Technology | Version policy | Responsibility |
| --- | --- | --- | --- |
| Shared UI | Flutter | Pin Flutter 3.44.7; use its bundled Dart SDK | Screens, navigation, view models, localization, accessibility |
| Host contract | Pigeon | 28.0.0, exact dev dependency | Generated Dart, Swift and C++ call/callback types |
| Shared core | Go | Go 1.26.5 from `go.mod` | NP/2, HTTP/3 WebTransport, packet tunnel, lifecycle, metrics |
| Windows UI host | Flutter Windows plugin, C++ | Visual Studio toolchain supported by Flutter 3.44.7 | Pigeon adapter and bounded named-pipe client |
| Windows privileged host | Existing Go Windows service | Same Go module | Wintun, routes, DPAPI, recovery journal, ClientCore |
| iOS UI host | Swift Flutter plugin | Swift 6 / Xcode-supported toolchain | Pigeon adapter, VPN manager, Keychain/profile adapter |
| iOS privileged host | `NEPacketTunnelProvider` + `NP2Mobile.xcframework` | iOS 16+ | utun settings, ClientCore, reconnect, provider diagnostics |
| HTTP/3 | `quic-go` + `webtransport-go` | Versions pinned in `go.mod` | QUIC/TLS 1.3 and WebTransport |

No state-management, service-locator or navigation framework is added for the
first slice. Flutter SDK primitives and explicit constructor injection are
sufficient. A new runtime dependency requires a documented need and review.

### 3.1 Why Flutter is limited to the application layer

Flutter supports a shared iOS and Windows codebase and documented platform
integration, but neither Wintun/Windows-service ownership nor Apple's Packet
Tunnel lifecycle belongs inside a Dart process. Native hosts preserve OS
security and lifecycle semantics while the shared UI removes duplicated screen
and state-management implementations.

---

## 4. Architecture

```text
                         shared source
┌──────────────────────────────────────────────────────────┐
│ Flutter app                                              │
│ screens · view models · localization · diagnostics view │
└──────────────────────────┬───────────────────────────────┘
                           │ Pigeon Host API v1
              ┌────────────┴────────────┐
              │                         │
┌─────────────▼──────────────┐  ┌───────▼──────────────────┐
│ Windows native plugin C++ │  │ iOS native plugin Swift │
│ bounded pipe adapter      │  │ NETunnelProviderManager │
└─────────────┬──────────────┘  └───────┬──────────────────┘
              │ framed local IPC        │ provider messages /
              │                         │ OS VPN lifecycle
┌─────────────▼──────────────┐  ┌───────▼──────────────────┐
│ LocalSystem Go service    │  │ PacketTunnelProvider     │
│ Wintun · routes · DPAPI   │  │ utun · Keychain refs     │
└─────────────┬──────────────┘  └───────┬──────────────────┘
              │                         │
              └────────────┬────────────┘
                           │ Go ClientCore
              ┌────────────▼────────────┐
              │ strict HTTP/3/WT + NP/2 │
              │ TCP/UDP flows · metrics │
              └─────────────────────────┘
```

### 4.1 Dependency direction

Dependencies point inward:

```text
Flutter screens -> Flutter application state -> Host API interfaces
native plugins -> generated Host API interfaces
platform hosts -> ClientCore interfaces
ClientCore -> NP/2 protocol and carrier packages
```

The Go ClientCore must not import Flutter, Swift, C++, Wintun service IPC or UI
packages. Carrier packages must remain unable to parse destination policy or
authenticate users independently.

### 4.2 Process ownership

| Concern | Flutter | Windows service | iOS extension |
| --- | :---: | :---: | :---: |
| Render UI | ✅ | — | — |
| Persist root secret | ❌ | DPAPI | Keychain reference |
| Select profile | Request only | Authoritative | Native app store authoritative |
| Establish NP/2 session | ❌ | ✅ | ✅ |
| Create TUN adapter | ❌ | Wintun | utun supplied by Network Extension |
| Install routes/DNS | ❌ | ✅ | `setTunnelNetworkSettings` |
| Reconnect | Observe only | ✅ | ✅ |
| Cleanup after UI crash | ❌ | ✅ | ✅ |

---

## 5. Shared ClientCore contract

The current `mobile/np2mobile` implementation contains reusable lifecycle and
packet logic. It is refactored behind an instance-based package instead of
adding more platform behavior to its package-level singleton.

Illustrative Go interface:

```go
type ClientCore interface {
	ValidateProfile(profileJSON []byte, secret []byte) error
	Connect(ctx context.Context, config ConnectConfig) (SessionSnapshot, error)
	AttachPacketTunnel(ctx context.Context, tunnel PacketTunnel) error
	NetworkChanged(ctx context.Context) (MigrationSnapshot, error)
	Snapshot() SessionSnapshot
	Close(ctx context.Context) error
}
```

Required properties:

- each tunnel instance owns its context, goroutines, queues and counters;
- `Connect`, `NetworkChanged` and `Close` are serialized by the native host;
- cancellation ownership is explicit;
- every input, allocation, queue, retry and deadline is bounded;
- internal errors are mapped to stable categories before crossing Host API;
- no mutable global session is introduced;
- tests inject clocks, randomness, dialers and packet tunnels;
- the first implementation accepts exactly one configured carrier kind:
  `http3_webtransport`.

The existing gomobile facade may remain as a compatibility adapter. New code
must depend on the instance interface, not on a process-global controller.

---

## 6. Host API v1

### 6.1 Contract source

`clients/unified/plugin/pigeons/client_host_api.dart` is the source of truth for
the Flutter/native application contract. Pigeon-generated Dart, Swift and C++
files stay in that same plugin package, are checked in and are verified as
reproducible outputs from Pigeon 28.0.0. Generated files are never manually
edited or mixed with output from another Pigeon version.

The Host API is not the NP/2 wire protocol. It is a local application API.

### 6.2 Versioning

- API version is `1.0` for the first slice.
- A major mismatch fails closed with `UNSUPPORTED_API_VERSION`.
- Minor versions are additive: new optional fields, enum values and methods.
- Unknown enum values map to `unknown`, never to a successful default.
- The UI calls `getCapabilities()` before every other operation after startup.
- Native hosts remain backward-compatible with all Host API minor versions
  shipped in the same installed major version.

### 6.3 Methods

| Method | Input | Result | Mutating | First slice |
| --- | --- | --- | :---: | :---: |
| `getCapabilities` | API major/minor | platform, versions, supported features | No | ✅ |
| `listProfiles` | none | redacted profile summaries | No | ✅ |
| `importProfile` | onboarding value | redacted imported profile | Yes | ✅ |
| `selectProfile` | profile ID | selected profile | Yes | ✅ |
| `removeProfile` | profile ID, force flag | empty success | Yes | ✅ |
| `connect` | profile ID, operation ID | accepted state | Yes | ✅ |
| `disconnect` | operation ID | accepted state | Yes | ✅ |
| `getStatus` | none | authoritative tunnel snapshot | No | ✅ |
| `getDiagnostics` | bounded options | redacted diagnostics snapshot | No | ✅ |
| `listRoutes` | none | merged route summaries | No | Later |

Status changes are delivered through a generated host-to-Flutter callback API.
The UI also refreshes `getStatus()` when it resumes, because callbacks are not
durable.

### 6.4 Core data types

Illustrative Pigeon model:

```dart
class HostApiVersion {
  int major;
  int minor;
}

class ProfileSummary {
  String id;
  String displayName;
  String serverIdentity;
  String host;
  bool selected;
  bool hasCredential;
  String origin;
}

class TunnelStatus {
  String state;
  String? profileId;
  String carrier;
  int connectedAtUnixMs;
  int uploadBytesPerSecond;
  int downloadBytesPerSecond;
  int uploadTotalBytes;
  int downloadTotalBytes;
  HostError? lastError;
  int sequence;
}
```

No Host API result may contain a root secret, complete onboarding URI, DPAPI
ciphertext, Keychain persistent reference, private route token or raw packet.

### 6.5 Stable errors

```dart
class HostError {
  String code;
  String stage;
  String message;
  bool retryable;
  String operationId;
}
```

Initial stable codes include:

| Code | Example stage | Meaning |
| --- | --- | --- |
| `HOST_UNAVAILABLE` | `HOST_IPC` | Native host cannot be reached. |
| `UNSUPPORTED_API_VERSION` | `HOST_NEGOTIATION` | UI/host major versions differ. |
| `INVALID_PROFILE` | `PROFILE_VALIDATION` | Onboarding/profile input is invalid. |
| `CREDENTIAL_UNAVAILABLE` | `CREDENTIAL_LOAD` | DPAPI/Keychain credential cannot be loaded. |
| `NO_SAFE_UPLINK` | `ENDPOINT_ROUTE` | Safe physical endpoint exclusion is unavailable. |
| `DNS_FAILED` | `DNS_RESOLUTION` | Carrier host resolution failed. |
| `UDP_UNREACHABLE` | `QUIC_HANDSHAKE` | UDP path cannot establish QUIC. |
| `TLS_FAILED` | `TLS_HANDSHAKE` | Certificate or TLS negotiation failed. |
| `HTTP3_TIMEOUT` | `WEBTRANSPORT_CONNECT` | HTTP/3/WebTransport deadline expired. |
| `NP2_AUTH_FAILED` | `NP2_AUTHENTICATION` | Carrier succeeded but NP/2 authentication failed. |
| `TUN_SETUP_FAILED` | `TUN_SETUP` | Wintun/utun configuration failed. |
| `CANCELLED` | any | The owning operation was cancelled. |
| `INTERNAL` | `HOST_INTERNAL` | Redacted unexpected failure. |

User messages are localized in Flutter from `code` and `stage`. The native
message is concise, redacted and suitable for diagnostics; it is not rendered
as an untrusted stack trace.

### 6.6 Command semantics

- Every mutating command has an opaque operation ID of 1..64 printable ASCII
  characters.
- Repeating `connect` with the same operation ID is idempotent.
- `connect` while connected to the same profile returns the current state.
- Connecting another profile first performs bounded native disconnect/cleanup.
- `disconnect` is idempotent from every state.
- A response means the operation was accepted or rejected. Completion is
  represented by authoritative status events/snapshots.
- At most one lifecycle mutation executes at a time.

### 6.7 Input bounds

| Input | Bound |
| --- | ---: |
| Onboarding value | 16 KiB UTF-8 |
| Profile ID | 128 bytes |
| Display name | 128 Unicode scalar values |
| Host API request | 256 KiB serialized |
| Host API response | 256 KiB serialized |
| Diagnostics entries | 256 |
| One diagnostic message | 512 UTF-8 bytes |
| Concurrent local IPC clients | 16 |

Unknown fields, duplicate fields, invalid UTF-8, trailing data, invalid enum
values and oversized frames are rejected before side effects.

---

## 7. Tunnel state machine

The UI consumes one cross-platform state model:

```text
disconnected -> connecting -> connected
      ^              |            |
      |              v            v
      +----------- failed <- reconnecting
      ^                           |
      +------ disconnecting <-----+
```

| State | Meaning |
| --- | --- |
| `disconnected` | No active NP/2 session and no active tunnel routes. |
| `connecting` | Native host is validating, dialing, authenticating or installing the tunnel. |
| `connected` | NP/2 is authenticated and packet routing is operational. |
| `reconnecting` | Tunnel stays fail-closed while the host restores the same HTTP/3 carrier. |
| `disconnecting` | Native cleanup is in progress. |
| `failed` | The attempt ended with a stable error; cleanup is complete or explicitly reported pending. |

`connected` must never be emitted after only a successful QUIC or NP/2
handshake. It requires usable packet-tunnel setup. Every snapshot carries a
monotonic sequence so delayed callbacks cannot overwrite newer state.

### 7.1 Strict HTTP/3 behavior

For the first candidate on both platforms:

- configured carrier set contains one HTTP/3 WebTransport endpoint;
- carrier pool target is one;
- WebRTC and HTTPS connector functions are not constructed;
- no stored compatibility route is dialed;
- failure is reported with its HTTP/3 stage;
- diagnostics state `http3-only`; the UI exposes no switch.

This temporarily differs from the existing adaptive iOS production behavior.
The legacy iOS client remains unchanged until the replacement candidate is
accepted. A later multi-carrier milestone requires a separate specification
change and tests; it cannot enter as a compatibility fallback.

### 7.2 Network transition and reconnect

The native host, not Flutter, owns reconnects.

1. Detect an OS network-path transition or carrier loss.
2. Move to `reconnecting` and keep application traffic fail-closed.
3. Probe the existing authenticated session with bounded NP/2 `PING/PONG`.
4. If the probe fails, close it and create a new HTTP/3 WebTransport session.
5. Reattach new flows atomically after NP/2 authentication.
6. Stop after six attempts or a 30-second overall deadline.
7. Use full-jitter backoff capped at 8 seconds.
8. On exhaustion, stop the packet tunnel, clean routes, and report the last
   stable stage error.

There is no same-flow byte resumption claim. No reconnect attempt may use a
different carrier.

---

## 8. Platform hosts

### 8.1 Windows

The existing LocalSystem service remains authoritative for profiles, DPAPI,
Wintun, DNS, routes and the recovery journal. The Flutter Windows runner is
unelevated.

The C++ plugin communicates only with the local service over a bounded named
pipe. Service IPC remains an internal adapter and may retain strict framed JSON
initially, provided it gains:

- explicit API version negotiation;
- structured stable errors;
- rejection of remote clients;
- an explicit least-privilege security descriptor;
- no secret-returning method;
- bounded connect/read/write deadlines;
- client identity/security tests;
- preservation of the 256 KiB frame and 16-client limits.

The Windows plugin uses one request and one response per connection. Each is a
four-byte little-endian length followed by strict UTF-8 JSON. The plugin waits
at most 1.5 seconds for the local pipe and applies one 12-second deadline to
the complete write/read exchange. Partial I/O is completed within that same
deadline; a zero, truncated or larger-than-256-KiB frame fails closed before
JSON decoding.

SYSTEM and Administrators require administrative access. Unelevated local
interactive users require only the rights needed to invoke the client API.
Anonymous, network and remote-pipe access are denied. The exact descriptor is
an implementation security review item; the current broad authenticated-user
grant must not be copied without review.

The safe sequence remains:

1. validate selected profile and credential;
2. resolve a safe physical uplink;
3. install and journal endpoint exclusions;
4. dial and authenticate strict HTTP/3 NP/2;
5. create/configure Wintun;
6. install DNS and tunnel routes;
7. attach ClientCore packet processing;
8. emit `connected`.

Any failure rolls back only changes owned by that operation. Stale journal
recovery runs before a new connection.

### 8.2 iOS

The Flutter iOS runner embeds a native Swift plugin. The Packet Tunnel remains
a separate Network Extension target and does not embed or start a Flutter
engine.

The Swift host:

- reads existing profile metadata from the current native store;
- resolves existing Keychain persistent references without exposing them to
  Dart;
- owns `NETunnelProviderManager` creation/loading;
- starts/stops `NETunnelProviderSession`;
- maps `NEVPNStatusDidChange` to Host API states;
- requests bounded redacted diagnostics with provider messages.

The extension:

1. validates profile and Keychain secret;
2. establishes and authenticates strict HTTP/3 NP/2;
3. calls `setTunnelNetworkSettings` and waits for completion;
4. attaches the duplicated tunnel file descriptor to ClientCore;
5. completes `startTunnel` only after the data path is usable;
6. owns path-change handling, reconnect and final cancellation.

Flutter never calls the gomobile API directly.

---

## 9. Persistence and migration

### 9.1 Windows migration

- Existing `%ProgramData%\NeProto` profile metadata remains readable.
- Existing DPAPI local-machine ciphertext remains the credential source.
- Existing selected-profile identity and installation identity are preserved.
- Schema migration is transactional, versioned and idempotent.
- Before replacement, a backup of changed metadata is created in the existing
  service-controlled data directory.
- Rollback to the legacy UI must not require credential reimport.

### 9.2 iOS migration

- Existing profile metadata is read through the current Swift model/store.
- Existing Keychain items and persistent references remain authoritative.
- Root secrets are never copied to `UserDefaults`, Flutter preferences or a
  Dart database.
- Suppressed/removed cluster profile state is preserved.
- Migration can be rerun without duplicate profiles or Keychain items.
- Removing a profile deletes its credential only after native store mutation
  succeeds and only when no retained profile references it.

### 9.3 Flutter persistence

Flutter may persist only non-sensitive presentation preferences such as locale,
theme and last-opened screen. It must not persist onboarding values, secrets,
private catalogue material or raw diagnostics.

---

## 10. Security and privacy

### 10.1 Trust boundaries

| Boundary | Threat | Required control |
| --- | --- | --- |
| Onboarding -> UI | Oversized/malformed secret-bearing input | In-memory handoff, 16 KiB cap, native validation |
| Flutter -> native plugin | Compromised/stale UI | API version gate, typed methods, bounds, no secret reads |
| Windows plugin -> service | Local untrusted process | local-only pipe, explicit ACL, identity checks, rate/deadline bounds |
| iOS app -> extension | Stale/spoofed provider data | OS-managed provider session, bounded messages, profile-ID validation |
| Native host -> network | DPI/MITM/untrusted server | normal QUIC/TLS validation plus NP/2 authentication |
| Diagnostics -> UI/logs | Credential or traffic disclosure | structured allow-list and redaction tests |

### 10.2 Required controls

- No TLS/DTLS verification bypass.
- No custom cryptography.
- No secret in Git, logs, process arguments, generated error messages or crash
  breadcrumbs.
- Onboarding values are zeroed or released as soon as native import completes.
- Profile secrets never cross from native host back to Flutter.
- No target dial occurs before NP/2 authentication and destination-policy
  validation.
- All native lifecycle calls are cancellable and bounded.
- All host callbacks are treated as untrusted at the Flutter boundary.
- Debug builds obey the same secret-redaction rules as release builds.

---

## 11. Flutter application behavior

### 11.1 Screens in the first slice

| Screen | Required behavior |
| --- | --- |
| Home | selected profile, state, one connect/disconnect control, HTTP/3 carrier, live basic traffic |
| Profiles | native-backed list, import, select, remove |
| Diagnostics | stable stage/code, timestamps, host/core versions, bounded redacted events |

No route editor, cluster administration or transport-selection screen is part
of the first slice.

### 11.2 Application state

One `ClientSessionController` owns UI state and depends on an abstract
`ClientHost` interface. Widgets do not invoke platform channels directly.

```dart
abstract interface class ClientHost {
  Future<HostCapabilities> getCapabilities();
  Future<List<ProfileSummary>> listProfiles();
  Future<ProfileSummary> importProfile(String onboardingValue);
  Future<void> connect(String profileId, String operationId);
  Future<void> disconnect(String operationId);
  Future<TunnelStatus> getStatus();
  Stream<TunnelStatus> watchStatus();
}
```

The controller treats the native snapshot as authoritative. It may optimistically
disable buttons while a command is in flight, but it must not fabricate
`connected`, schedule reconnects or infer a fallback.

### 11.3 Accessibility and localization

- Russian is complete for the first release; strings are externalized for
  English and future locales.
- Controls have semantic labels and state values.
- State is never conveyed by color alone.
- Windows keyboard navigation and iOS Dynamic Type are release gates.
- Motion respects platform reduced-motion preferences.

---

## 12. Proposed project structure

```text
clients/
  unified/
    app/                          Flutter application
      lib/
        application/             controllers and immutable UI state
        host/                    abstract ClientHost + generated adapter
        l10n/                    localized strings
        screens/                 home, profiles, diagnostics
        widgets/                 shared presentational components
      test/                      unit and widget tests
      integration_test/          fake-host application journeys
      pubspec.yaml
    plugin/
      pigeons/
        client_host_api.dart     canonical Host API v1 declarations
      lib/                       Dart plugin facade
      ios/Classes/               Swift host adapter
      windows/                   C++ named-pipe adapter
    tool/
      generate_host_api.*        reproducible Pigeon generation
internal/
  clientcore/                    instance-based cross-platform Go core
  clienthost/                    stable status/error mapping
  windowsclient/                 Windows service/platform adapter
mobile/
  np2mobile/                     gomobile compatibility facade
clients/ios/
  PacketTunnel/                  native extension retained
```

Generated Flutter runner files stay inside `clients/unified/app`. The existing
`clients/windows/NeProto.App` and native SwiftUI views remain untouched until
cutover approval.

---

## 13. Code style

### 13.1 Dart

- `dart format` is mandatory.
- Immutable state and explicit constructor injection are preferred.
- Platform exceptions are mapped once in the Host adapter.
- Widgets contain presentation logic only.
- Avoid `dynamic`, global service locators and unbounded stream subscriptions.

```dart
final class ClientSessionController extends ChangeNotifier {
  ClientSessionController(this._host);

  final ClientHost _host;
  TunnelStatus? _status;

  Future<void> refresh() async {
    _status = await _host.getStatus();
    notifyListeners();
  }
}
```

### 13.2 Go

- Follow the repository conventions in `AGENTS.md`.
- Start behavior changes with a failing focused test.
- Use sentinel/stable categories rather than returning raw internal errors.
- Pass `context.Context` into every blocking operation.
- Inject clocks, dialers and packet tunnels in tests.
- Run `gofmt`; generated bindings are excluded from manual edits.

### 13.3 Swift and C++

- Swift concurrency/lifecycle ownership is explicit and main-actor UI work is
  separated from extension work.
- C++ uses RAII for handles and buffers; no owning raw pointers.
- Both adapters validate generated-model values again before native side
  effects.
- Neither adapter logs onboarding values or provider configuration blobs.

---

## 14. Development commands

Flutter is not installed on the current Windows development machine at the
time this specification was written. Installing/pinning it is a separate
bootstrap task after this specification is approved.

### 14.1 Shared Go checks on Windows

```powershell
gofmt -w ./cmd ./internal ./tests ./mobile
go test -race -coverprofile=coverage.out ./...
go vet ./...
go build ./cmd/...
govulncheck ./...
```

These commands must not launch `NeProto.exe`, `NeProto.Service.exe`, setup,
Wintun or route-changing smoke workflows on this machine.

### 14.2 Flutter checks

```powershell
Set-Location clients/unified/app
flutter pub get
dart format --output=none --set-exit-if-changed .
flutter analyze
flutter test
flutter build windows --release
```

`flutter build windows` is a compile/package check only. The result is not
launched on this machine.

### 14.3 iOS framework and app checks on the Mac

```bash
cd /Users/intimnyjprysik/Downloads/LyntraMessenger/Neproto
clients/ios/Scripts/build-frameworks.sh
cd clients/unified/app
flutter pub get
flutter analyze
flutter test
flutter build ios --release --no-codesign
```

The signed Packet Tunnel application is then built from its generated Xcode
workspace/project with the real development team and extension entitlements.
Exact signing commands stay in a script and must not contain secrets.

---

## 15. Testing strategy

### 15.1 Test pyramid

| Layer | Tests | Required evidence |
| --- | --- | --- |
| Host contract | generated API compatibility, bounds, unknown enum/version | Dart + native unit tests |
| Flutter state | fake-host lifecycle, stale sequences, errors, restart sync | Dart unit tests |
| Flutter UI | home/profile/diagnostic states, accessibility | widget/golden tests |
| ClientCore | HTTP/3-only selection, cancellation, reconnect, metrics | Go unit/race tests |
| Windows host | pipe ACL/locality, DPAPI migration, route rollback | Windows CI + separate test PC |
| iOS host | Keychain migration, provider messages, VPN state mapping | Swift tests + physical iPhone |
| End to end | import, connect, media traffic, transition, disconnect | separate Windows PC + physical iPhone |

### 15.2 Required focused tests before implementation code

1. Host rejects unsupported API major version.
2. Host never returns credential material.
3. Strict policy constructs only the HTTP/3 connector.
4. An HTTP/3 failure cannot invoke WebRTC or HTTPS test dialers.
5. `connected` is emitted only after the packet tunnel is usable.
6. Stale status sequence cannot replace a newer UI state.
7. Repeated connect/disconnect operation IDs are idempotent.
8. Reconnect is bounded by attempt count and total deadline.
9. Existing Windows profile/DPAPI records load without rewrite.
10. Existing iOS profile/Keychain references load without secret exposure.
11. Route/TUN failure rolls back native changes.
12. All error strings and diagnostics pass secret-redaction fixtures.

### 15.3 Real-device acceptance scenarios

The development machine is excluded from Windows runtime execution.

| Scenario | Windows test PC | Physical iPhone |
| --- | :---: | :---: |
| Existing profile appears without reimport | ✅ | ✅ |
| Strict HTTP/3 connect succeeds | ✅ | ✅ |
| UI shows `http3_webtransport` | ✅ | ✅ |
| YouTube media loads and seeks for 10 minutes | ✅ | ✅ |
| Telegram media upload/download works | ✅ | ✅ |
| Instagram feed/video continues for 10 minutes | ✅ | ✅ |
| Wi-Fi reconnect does not select fallback | ✅ | ✅ |
| Wi-Fi to cellular transition reconnects same carrier | N/A | ✅ |
| UDP/443 blocked yields an HTTP/3-stage failure | ✅ | ✅ |
| UI restart resynchronizes active native status | ✅ | ✅ |
| Disconnect removes owned routes/tunnel state | ✅ | ✅ |

Packet capture or server logs must show only QUIC/HTTP/3 carrier attempts for
the first candidate. UI success alone is insufficient evidence.

---

## 16. Observability

Each connection operation records bounded stage transitions with monotonic
timestamps:

```text
PROFILE_VALIDATION
CREDENTIAL_LOAD
DNS_RESOLUTION
ENDPOINT_ROUTE
QUIC_HANDSHAKE
TLS_HANDSHAKE
WEBTRANSPORT_CONNECT
NP2_AUTHENTICATION
TUN_SETUP
PACKET_FORWARDING
```

Diagnostics include app version, host version, ClientCore version, OS version,
carrier policy, current carrier, stage durations, reconnect count, byte totals
and stable errors. They exclude hostnames when privacy mode requires it,
secrets, onboarding values, target destinations and payloads.

The UI and native host include the same operation ID so a device report can be
correlated with server-side timestamps without embedding credentials.

---

## 17. Delivery and cutover boundaries

The implementation proceeds only after approval of this specification. The
subsequent plan must split work into independently verified vertical slices.

Cutover is allowed only when:

- existing profile migration passes on both platforms;
- the strict HTTP/3 real-device matrix passes;
- Windows route cleanup is proven on the separate test PC;
- iOS signing, entitlement, install and Packet Tunnel execution pass on the
  physical iPhone;
- rollback to the previous client package is documented and tested;
- no server deployment or protocol migration is required for the client
  update.

Until cutover, the new client is a candidate and does not replace the published
stable client.

---

## 18. Success criteria

The specification is satisfied when all of the following are true:

- At least 90% of non-generated presentation/application Dart code is shared
  between Windows and iOS.
- Windows and iOS expose the same Host API v1 behavior and stable errors.
- Both platforms connect only through HTTP/3 WebTransport in the first
  candidate; alternate-carrier dial count is exactly zero.
- Existing users retain profiles, selected server and credentials without
  reimport.
- Flutter never receives or persists a root secret.
- A UI crash/restart cannot leave native lifecycle state unknown to the next UI
  launch.
- Every reconnect loop, frame, queue, callback buffer and diagnostic list is
  bounded.
- Go race tests, vet, builds, Flutter analyze/tests and native unit tests pass.
- The real Windows test PC and physical iPhone acceptance matrix passes with
  device/server evidence.
- The Windows client is never executed on the development machine.
- Publication occurs only after candidate evidence is reviewed.

---

## 19. Risks and mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Flutter UI and native VPN state diverge | Wrong button/state, unsafe assumptions | Native snapshot authoritative; sequence numbers; resume refresh |
| iOS extension lifecycle is tied to Flutter | Tunnel terminates or cannot reconnect | Extension remains native and Flutter-free |
| Windows local process abuses service IPC | Machine-wide VPN manipulation | local-only pipe, explicit ACL/identity review, bounded methods |
| Migration damages credentials | Users cannot connect or roll back | read-first adapters, idempotent schema, backup, no secret relocation |
| Rewrite hides existing HTTP/3 fault | New UI repeats current timeout | staged errors, device/server evidence, no fallback |
| Shared core becomes platform-coupled | Future platforms require forks | instance interface and inward dependency direction |
| Added toolchain destabilizes releases | Build/reproducibility failures | pin Flutter/Pigeon, generated-code checks, isolated candidate pipeline |

---

## 20. Approval decisions

Approval of this specification approves these architectural choices:

1. Flutter 3.44.7 for shared Windows/iOS UI.
2. Native privileged hosts remain responsible for VPN lifecycle.
3. Existing Go logic is refactored into an instance-based ClientCore rather
   than rewritten in Dart.
4. Pigeon is the typed Flutter/native Host API source.
5. Existing Windows DPAPI and iOS Keychain/profile stores are migrated in
   place without exposing secrets.
6. The first candidate is strictly HTTP/3 WebTransport on both platforms.
7. Windows runtime validation occurs only on another PC; iOS runtime validation
   occurs on the physical iPhone through the Mac.

After approval, the next artifact is a detailed implementation plan and TDD
task breakdown. No production client implementation starts before that gate.

---

## 21. Authoritative sources

- Flutter supported deployment platforms:
  https://docs.flutter.dev/reference/supported-platforms
- Flutter platform integration:
  https://docs.flutter.dev/platform-integration
- Flutter platform channels and Pigeon:
  https://docs.flutter.dev/platform-integration/platform-channels
- Pigeon 28.0.0 package and generation guidance:
  https://pub.dev/packages/pigeon/versions/28.0.0
- Apple `NEPacketTunnelProvider`:
  https://developer.apple.com/documentation/networkextension/nepackettunnelprovider
- Apple `NETunnelProvider` application communication:
  https://developer.apple.com/documentation/networkextension/netunnelprovider
- Apple `startTunnel` lifecycle:
  https://developer.apple.com/documentation/networkextension/nepackettunnelprovider/starttunnel%28options%3Acompletionhandler%3A%29
- Microsoft named-pipe security and access rights:
  https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights
- NP/2 architectural contract: `docs/SPEC.md`
- Existing Windows client contract: `docs/WINDOWS-CLIENT-SPEC.md`
- Existing iOS mobile data-plane contract: `docs/NP2-2.1-IOS-SPEC.md`
