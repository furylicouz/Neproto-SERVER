# Spec: Neproto Chameleon NP/2

Status: NP/2 v2.2 production implementation in release verification

Date: 2026-07-18

The normative production contract is
[`NP2-2.2-PRODUCTION-SPEC.md`](NP2-2.2-PRODUCTION-SPEC.md). This document is the
short architectural overview; the production specification wins if the two
documents differ.

## Objective

Create an original personal proxy protocol named **Neproto Chameleon NP/2**. NP/2 is not a VLESS, Trojan, SOCKS, Hysteria, or Shadowsocks wire-format variant. It defines its own authenticated session, multiplexed stream lifecycle, cell encoding, cover scheduling, carrier selection, and failure behavior.

The local SOCKS5 listener is only an optional desktop application adapter.
SOCKS bytes never appear on the public wire. The iOS NP/2 v2.2 data plane
connects its userspace TCP/IP stack directly to encrypted TCP streams and UDP
associations, using a carrier datagram fast path when negotiated.

## Product Promise

NP/2 aims to reduce stable protocol signatures and remain usable when either UDP or TCP is impaired. It does not promise permanent invisibility or guaranteed superiority to every Xray configuration. Claims are accepted only after comparative packet-capture, availability, latency, throughput, and overhead measurements.

## Architecture

```text
Applications
    |
loopback SOCKS5
    |
Chameleon Core
    |-- Session Authenticator (challenge-response)
    |-- Stream Multiplexer
    |-- Cell Codec
    |-- Adaptive Cover Engine
    |-- Authenticated Carrier Racer
    |
    |-- HTTP/3 carrier: QUIC / WebTransport stream + datagrams on UDP 443
    |-- WebRTC carrier: ICE / DTLS / SCTP / reliable + unreliable DataChannels
    `-- HTTPS carrier: TLS 1.3 / WebSocket over TCP 443 compatibility path
            |
        NP/2 server
            |
        target TCP services
```

Both carriers transport identical NP/2 cells. Carrier code cannot parse destination addresses or make authentication decisions.

## Differentiators

1. **Server-challenge authentication.** The server contributes fresh entropy before a client can authenticate, avoiding timestamp-only first-packet authentication and making captured responses unusable in a new session.
2. **Carrier-independent protocol.** One logical session model runs over actual WebRTC and actual HTTPS, with automatic fallback rather than a renamed TCP proxy.
3. **Multiplexed stream cells.** Many target TCP streams share one authenticated carrier session.
4. **Session-specific cell mapping.** Cell type codes and padding buckets are derived from the authenticated session seed, so there is no stable NP/2 application magic inside carriers.
5. **Adaptive cover budget.** Dummy cells, padding, burst timing, and profile transitions are bounded by explicit latency and bandwidth budgets.
6. **Valid cover state machines.** UDP traffic uses WebRTC ICE/DTLS/SCTP semantics; TCP traffic uses valid TLS/HTTP/WebSocket semantics. No arbitrary bytes are appended outside those protocols.
7. **Measured profiles.** Cover profiles are versioned data and evaluated against packet traces; names such as `interactive` describe behavior without claiming to impersonate a specific commercial game.

## Scope

### Version 2.2 production scope

- Windows and Linux command-line clients plus a native iOS Packet Tunnel.
- Ubuntu/Debian amd64 and arm64 server bundles for bare metal and Docker.
- Local loopback SOCKS5 adapter for desktop and direct utun integration on iOS.
- Multiplexed TCP streams, reliable UDP associations, and bounded unreliable
  datagram acceleration.
- Real WebTransport/HTTP3, dual-channel WebRTC, and HTTPS/WebSocket carriers.
- Bounded staggered racing where only a fully authenticated, extension-policy
  compliant candidate can win; all losing candidates are closed.
- Challenge-response authentication using a random 256-bit root secret.
- Destination policy that blocks loopback, link-local, multicast, unspecified, and private ranges by default.
- Cover profiles: `quiet`, `web`, and `interactive`.
- Maximum dummy/padding overhead default 30%, configurable 0..100%.
- Per-user credentials, v2 QR onboarding, certificate automation, an
  interactive `np` server manager, backups, rollback, and a normal decoy site.
- Structured counters without payload, destination, credential, or raw packet logging.
- Process-wide and per-user session/TCP/UDP ceilings, packet/byte/DNS/target
  rates, per-association target bounds, and first-reply UDP anti-amplification.

Release verification, performance SLOs, packet-capture comparisons, and staged
rollout gates are tracked in
[`NP2-2.2-PRODUCTION-PLAN.md`](NP2-2.2-PRODUCTION-PLAN.md). An unchecked gate is
not described as production-proven.

### Onboarding v2 location metadata

An `np2://import/v2/` profile may include an optional `region` string. The
server copies this value from the client-visible cluster node whose public
identity matches `server_identity`. Clients use it only for presentation, such
as a localized location title or country flag; it never participates in
authentication, routing, carrier selection, or TLS verification.

For a newly created local master, the server should replace its historical
`Primary` placeholder with a two-letter ISO country code determined locally
from the installed, verified GeoIP database and the configured public server
address. This lookup must not call an external geolocation service. An explicit
administrator region always takes precedence.

`region` is UTF-8, trimmed, contains no control characters, and is limited to
64 Unicode scalar values. Version 1 onboarding profiles do not accept the
field. Clients must remain compatible with version 2 profiles that omit it and
must use a neutral location fallback when the value cannot be mapped to a
country.

### Post-v2.2 capability-gated development

NP/2 Constellation is an additive continuity-first multi-carrier extension. Its
normative development contract and ordered delivery gates are tracked in
[`NP2-CONSTELLATION-SPEC.md`](NP2-CONSTELLATION-SPEC.md) and
[`NP2-CONSTELLATION-IMPLEMENTATION-PLAN.md`](NP2-CONSTELLATION-IMPLEMENTATION-PLAN.md).
It is disabled unless both authenticated peers negotiate its capability; absent
that capability, the production v2.2 wire and lifecycle behavior above remains
unchanged.

### Explicitly not doing

The production cluster control plane, signed per-user server catalogue,
administrator/client route merge, SSH node enrolment, and bounded inter-node
NP/2 relay are specified in
[`NP2-CLUSTER-ROUTING-SPEC.md`](NP2-CLUSTER-ROUTING-SPEC.md). Cluster features
remain disabled unless authenticated peers negotiate `cluster_catalog_v1`.

- No VLESS/VMess/Trojan compatibility.
- No copied Xray configuration schema or packet layout.
- No proprietary game protocol impersonation without a complete compatible state machine.
- No custom cipher, hash, PRNG, or unauthenticated encryption.
- No TLS verification bypass or TLS 0-RTT authentication.
- No claims based solely on a visually random packet dump.

## Technology

| Component | Version | Role |
|---|---:|---|
| Go | 1.26.7 | Client, server, tests |
| Pion WebRTC | 4.2.16 | Standards-compliant WebRTC carrier |
| quic-go | 0.60.0 | QUIC and HTTP/3 transport |
| webtransport-go | 0.11.1 | WebTransport Extended CONNECT sessions and datagrams |
| Coder WebSocket | 1.8.15 | HTTPS carrier |
| Caddy | 2.11.4 | Certificate automation, decoy site, reverse proxy |

The module graph is pinned in `go.mod` and `go.sum`. Standard-library cryptography is preferred.

## NP/2 Session Protocol

All carrier messages are opaque binary messages. Decoders reject non-canonical varints, trailing data, truncation, invalid state transitions, unknown mandatory features, and data above configured limits.

### Authentication transcript

After the carrier is established:

1. Server sends `Challenge` containing a 32-byte CSPRNG nonce and supported protocol/profile bitsets.
2. Client sends `Response` containing a fresh 32-byte nonce, requested features,
   an optional 16-byte installation identifier when `FeatureDeviceIdentity` is
   selected, and `HMAC-SHA-256(auth_key, transcript)`.
3. Server verifies the response in constant time, derives a session seed, and sends `Confirm` with a server HMAC over the complete transcript.
4. Both sides derive independent header-map, padding, control, and directional cell-encryption keys using HKDF-SHA-256.

```text
auth_key    = HKDF(root_secret, "NP2 auth" || server_identity)
transcript  = protocol_version || carrier || server_nonce || client_nonce || features || optional_device_id
session_seed = HKDF(auth_key, "NP2 session" || transcript)
```

Authentication messages are capped at 512 bytes. A challenge expires after 15 seconds and is single-use. The server caps outstanding unauthenticated sessions globally and per source address.

`FeatureDeviceIdentity` is additive and server-first compatible. A client sends
the identifier only when the server advertises the feature. The identifier is
authenticated as part of the response transcript, contains no hardware value,
and is generated once per application installation. It groups parallel carrier
sessions into one logical device; it is not a fingerprint and is not treated as
a cryptographic secret. The administrative and accounting contract is defined
in [`NP2-USER-SESSIONS-SPEC.md`](NP2-USER-SESSIONS-SPEC.md).

### Session-specific type mapping

Canonical internal cell kinds are:

- `OPEN`, `OPEN_OK`, `OPEN_FAIL`
- `DATA`, `FIN`, `RESET`
- `WINDOW_UPDATE`
- `DUMMY`, `PROFILE`, `PING`, `PONG`, `GOAWAY`

A Fisher-Yates permutation driven by HKDF-expanded session material maps those kinds to one-byte wire codes. This is protocol agility, not a cryptographic security boundary. NP/2 v2.1 additionally protects every post-authentication cell with directional ChaCha20-Poly1305; TLS/DTLS remains an independent outer carrier boundary.

### Cell format

Fields use canonical unsigned varints unless otherwise stated:

```text
wire_type | stream_id | sequence | payload_length | padding_length | payload | random_padding
```

- Complete cell: at most 65,535 bytes.
- Payload: at most 32,768 bytes.
- Padding: at most 16,384 bytes and additionally constrained by the active budget.
- Stream IDs are odd for client-created streams and even for future server-created streams.
- Sequence numbers are strictly monotonic per stream; duplicates and regressions reset the stream.
- Stream 0 is reserved for session control.

### Stream lifecycle

`OPEN` payload contains command, address type, canonical address, port, and initial receive window. The server validates authentication and destination policy before dialing. `OPEN_FAIL` carries a small stable category, never an internal error string.

Flow control is credit based. A sender cannot exceed the peer's advertised per-stream window. Session and stream queue sizes are bounded. Receivers may coalesce consumed credit into fewer `WINDOW_UPDATE` cells because the credit value is additive; they must flush credit when the sender can be stalled and before a completed stream is retired. The mobile performance default is 2 MiB per stream, remains bounded by the 16 MiB protocol maximum, and does not change the cell wire format.

## Adaptive Cover Engine

The engine accepts real cells and produces a schedule of real, padded, and dummy carrier messages.

| Profile | Intended shape | Default budget | Latency ceiling |
|---|---|---:|---:|
| `quiet` | Minimal overhead, no periodic dummy cells | 5% | 2 ms |
| `web` | Asymmetric request/response bursts and bucketed records | 20% | 12 ms |
| `interactive` | Frequent small messages with bounded jitter and idle keepalive | 30% | 20 ms |

Rules:

- Production client and administrative UIs expose one automatic mode. They
  serialize the backward-compatible base value `web`; legacy imported
  `quiet` and `interactive` values remain decodable but are normalized to
  `web` when a runtime configuration is generated.
- Real cells approaching the latency ceiling bypass dummy scheduling.
- The latency ceiling shapes the start of an outbound burst, not every cell in
  that burst. After an idle boundary, the first real cell may receive the
  profile's bounded session-keyed delay; immediately following real cells use
  a zero-added-delay fast path until the burst becomes idle again. Padding and
  dummy budgets remain active. Per-cell sleeps must never impose a throughput
  ceiling on bulk or media transfers.
- Dummy and padding bytes are charged to a token bucket; budget exhaustion disables cover until tokens recover.
- Timing randomness comes from a session-keyed deterministic generator seeded with CSPRNG session entropy.
- Fixed-profile peers change profiles only through authenticated `PROFILE`
  control cells at defined state boundaries. Peers that select
  `CapabilityMosaicCover` additionally permit the deterministic local
  sender-side transitions defined below; no decoder state depends on them.
- Profile names are behavioral labels, not assertions that traffic is identical to a named application.

### Mosaic v2.2 adaptive scheduling

Peers may additively negotiate `CapabilityMosaicCover` inside the existing
post-authentication extension capability TLV. When selected, the sender adapts
its local fixed `web` or `interactive` cover definition among `web`,
`realtime`, and a zero-delay `stream` fast path using only bounded aggregate
outbound size/timing counters. `quiet` remains fixed. Unknown/unsupported
capabilities fall back to the original fixed profile without changing the base
handshake or cell format.

The normative thresholds, hysteresis, compatibility rules, and security
boundary are defined in [`NP2-MOSAIC-SPEC.md`](NP2-MOSAIC-SPEC.md).

### Forward-secret rekey barrier

Peers that select `CapabilityForwardSecrecy` bind their ephemeral X25519 key
shares to the authenticated extension exchange and derive fresh directional
cell keys. The key transition is a four-step barrier:

1. the client sends the selected extension parameters under the base cell keys;
2. the server sends message `3`, containing `ExtensionTLVForwardSecretConfirm`,
   under the base cell keys and then switches its record layer to the derived
   keys;
3. the client verifies that confirmation under the base cell keys, switches its
   record layer, and sends message `4`, containing
   `ExtensionTLVForwardSecretAck`, under the derived keys;
4. the server accepts application cells only after verifying message `4` under
   the derived keys.

Extension and continuity envelopes share one monotonically increasing message
ID namespace in each direction. A client that completes this barrier therefore
starts Constellation continuity control at message `5`; message `4` remains
reserved for the forward-secret acknowledgement.

The confirmation and acknowledgement use distinct keyed transcript labels.
Either missing proof, an unexpected message ID or TLV, or authentication under
the wrong key generation terminates extension negotiation. This ordering keeps
base-key and derived-key records from racing the asynchronous record reader and
does not permit a plaintext or unauthenticated overlap window.

## Carrier Contract

```go
type Carrier interface {
	Send(context.Context, []byte) error
	Receive(context.Context) ([]byte, error)
	Close() error
	Kind() CarrierKind
}
```

### WebRTC

- Real ICE connectivity checks, DTLS, SCTP, an ordered reliable binary
  DataChannel, and a separate unordered zero-retransmit datagram DataChannel.
- HTTPS signaling accepts an SDP offer and returns an SDP answer only after a bounded pre-auth request.
- The unreliable path is used only after authenticated v2.2 capability
  negotiation; reliable NP/2 records remain available for fallback.
- Server UDP port range is explicit and firewall-limited.

### HTTP/3

- QUIC/TLS 1.3 and WebTransport Extended CONNECT run on UDP 443 with normal
  hostname and certificate validation.
- Reliable WebTransport streams carry NP/2 cells; HTTP datagrams carry only
  bounded encrypted NP/2 datagram records after capability negotiation.
- Disabling HTTP/3 or its datagram kill switch preserves WebRTC/HTTPS paths.

### HTTPS

- TLS 1.3 with normal hostname and certificate validation.
- WebSocket compression disabled.
- Private high-entropy route behind Caddy; all other routes serve the decoy site.

### Hybrid selection

During the bounded Windows HTTP/3 isolation phase, profiles generated by the
Windows client use `carrier_policy=http3-only` and one carrier session. This
policy permits only the configured HTTP/3/WebTransport carrier for initial
connection, pool construction, and reconnection. HTTPS/WebSocket and WebRTC
routes remain stored for a later rollback but are never dialed as fallbacks.
If HTTP/3 is unavailable, Windows reports the HTTP/3 failure instead of
silently changing transport. iOS and generic desktop clients retain the normal
adaptive policy.

1. Runtime clients default to an authenticated adaptive race that prioritizes
   carriers with native unreliable datagrams: HTTP/3, then WebRTC, with HTTPS
   delayed as the route-compatible fallback. A cached HTTP/3 or WebRTC result
   may start first; a cached HTTPS result must not suppress a fresh datagram
   probe when HTTP/3 is configured.
2. `carrier_policy=performance` and the legacy `carrier_policy=udp-first` both
   use this adaptive selection. Diagnostic `ProbeAuto` uses the same bounded
   race for per-network measurement.
3. iOS, Windows, and desktop clients reuse the selected policy for the initial
   connection, warm carrier pool, and carrier migration. They must not pin a
   media session to HTTPS merely because HTTPS authenticated first on a prior
   network.
4. HTTPS may win the initial race to provide immediate connectivity. When a
   later authenticated warm probe exposes native unreliable datagrams, the
   runtime atomically promotes HTTP/3 (or WebRTC when HTTP/3 is unavailable) to
   the primary route. New TCP and UDP flows use the promoted carrier; existing
   streams drain on their original carrier for a bounded interval. This
   background probe gives HTTP/3 its bounded handshake window without letting
   the already-working HTTPS fallback cancel it. Promotion is automatic and
   must not require a user-visible transport mode.
5. A candidate wins or is promoted only after NP/2 authentication and required v2.2 extension
   policy succeeds; every losing carrier is closed.
6. On an iOS network change, first probe the current authenticated session with
   `PING/PONG`. If it survives, keep it. Otherwise authenticate a replacement.
7. Atomically route new TUN flows to the replacement session. Existing streams
   remain on the old session for a bounded 30-second drain; NP/2 does not claim
   cross-carrier byte-level stream resumption.
8. When the configured carrier bound permits it, a native-datagram primary
   keeps one independently authenticated, carrier-diverse compatibility
   standby. Unexpected primary loss promotes a healthy standby before the
   packet tunnel is declared terminal. The replacement pool is replenished in
   the background; no user-visible transport selection is introduced. A
   compatibility standby is excluded from ordinary TCP, UDP, and continuity
   scheduling while the native primary is healthy, so failover capacity cannot
   silently move media traffic back to the slower compatibility carrier.

When the selected carrier exposes only reliable NP/2 UDP associations, the
client preserves bounded reliable UDP as a last-resort compatibility path. It
does not reject application UDP/443 as a normal media strategy: production
media uses HTTP/3 or WebRTC datagrams whenever either carrier is available. The
client may report a degraded reliable-UDP state, but it must not create a retry
storm by repeatedly refusing the application's QUIC flows.

On Windows, endpoint-exclusion routes for the NP/2 server must be resolved via
an active physical uplink rather than an unrelated third-party tunnel adapter.
The exclusions are installed and durably journaled before the first carrier
dial, so a reconnect cannot be captured by stale full-tunnel routes. Only after
the carrier authenticates may the client create Wintun and install the NeProto
default routes. A failed dial rolls the prepared exclusions back. If no safe
physical route exists, connection fails before sending carrier traffic or
installing the NeProto default routes instead of silently nesting NP/2 inside
another VPN.

## Configuration and Commands

```text
neproto-client run --config <path>
neproto-client check --config <path>
neproto-client version

neproto-server run --config <path>
neproto-server check --config <path>
neproto-server generate-secret
neproto-server version
```

Secrets are base64url-encoded 32-byte values stored only in mode-0600 configuration files. They never appear in command arguments, repository files, logs, or generated client links.

## Security Boundaries

### Always

- Validate all network/config input, use CSPRNG and standard cryptography, compare authenticators in constant time, cap allocations/queues/timeouts, block unsafe destinations by default, run as an unprivileged user, and redact secrets.

### Ask first

- Exact domain and DNS, VPS package installation, firewall/SSH changes, private-range access, UDP proxying, TUN mode, ECH, or public user management.

### Never

- Custom cryptographic primitives, public backend bind, insecure TLS, arbitrary out-of-protocol bytes, unbounded replay/session caches, payload logging, or modification of the existing Zabbix service.

## Verification

- Unit and fuzz tests for authentication, transcript binding, permutation, varints, cells, flow control, cover budgets, profile transitions, SOCKS parsing, and destination policy.
- Race-enabled integration tests over real localhost WebSocket and Pion DataChannel carriers.
- End-to-end SOCKS TCP and RFC 1928 UDP ASSOCIATE requests over real localhost
  carriers, plus UDP-blocked carrier fallback.
- Active-probe checks: unrelated HTTPS requests serve decoy content; invalid signaling/auth never dials a target.
- Performance baseline before tuning: latency, throughput, allocations, goroutines, and actual cover overhead.
- Comparative captures against a selected Xray configuration. Results report observable differences without claiming universal DPI resistance.

## Success Criteria

- All tests, race detector, vet, vulnerability scan, and fuzz smoke tests pass.
- The staged 2-vCPU/2-GiB target sustains 100 authenticated sessions, 5,000 TCP
  streams, and 10,000 UDP associations without post-close resource growth.
- HTTP/3, WebRTC, and HTTPS each carry authenticated end-to-end traffic.
- Blocking UDP triggers HTTPS fallback within the configured deadline.
- Measured cover overhead never exceeds its configured budget over the test window.
- Replayed or cross-session authentication responses fail before any target dial.
- Public services are limited to approved HTTP(S), WebRTC UDP, SSH, and the pre-existing Zabbix port.

## Sources

- WebRTC Data Channels: https://www.rfc-editor.org/rfc/rfc8831.html
- WebRTC transports: https://www.rfc-editor.org/rfc/rfc8835.html
- TLS 1.3: https://www.rfc-editor.org/rfc/rfc8446.html
- WebSocket: https://www.rfc-editor.org/rfc/rfc6455.html
- HTTP Datagrams: https://www.rfc-editor.org/rfc/rfc9297.html
- Pion WebRTC release: https://github.com/pion/webrtc/releases/tag/v4.2.16
- Coder WebSocket release: https://github.com/coder/websocket/releases/tag/v1.8.15
- Caddy release: https://github.com/caddyserver/caddy/releases/tag/v2.11.4
- Go release history: https://go.dev/doc/devel/release
