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
