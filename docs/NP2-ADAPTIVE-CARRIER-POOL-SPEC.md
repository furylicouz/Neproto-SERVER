# NP/2 Adaptive Carrier Pool Specification

## Objective

Increase real mobile throughput without weakening NP/2 authentication, cell
encryption, endpoint privacy, or Mosaic cover behavior. The mobile runtime may
maintain a bounded pool of independent authenticated HTTPS carrier sessions and
assign each new inner TCP flow to the least-loaded healthy session.

This is flow-level distribution, not packet striping. One inner TCP flow always
stays on one NP/2 session, so byte ordering and existing server-side stream
semantics do not change. HTTP/3 remains a separately authenticated fallback and
is not mixed into the HTTPS pool until physical-device evidence shows that the
current network's UDP route is faster.

## Assumptions

1. Mobile `performance` profiles may keep two warm HTTPS carriers and scale to
   three only during concurrent or sustained high-bandwidth traffic.
2. `quiet` and `udp-first` profiles remain single-carrier by default.
3. Existing profiles remain valid. A value of `1` is the rollback mode and
   preserves current behavior exactly.
4. The production server keeps at least three authenticated sessions available
   per user. The packaged default is eight.
5. The client migrates profiles to adaptive mode without user input. Release
   acceptance still requires a physical A/B with at least 20 percent median
   throughput improvement; otherwise the release profile is rolled back to 1.

## Architecture and contracts

### Client configuration

Add one bounded optional field:

```json
{
  "max_parallel_carriers": 3
}
```

- Accepted range: `1..3`.
- Desktop default: `1`.
- Direct mobile `performance` default: `3` maximum, with adaptive activation.
- Mobile `quiet` or `udp-first` default: `1`.
- Unknown or out-of-range values fail strict profile validation.

### Session router

`tunstack.SessionRouter` owns an immutable snapshot of healthy routes:

```go
type sessionRoute struct {
    id            uint64
    open          streamOpenFunc
    activeStreams func() uint64
    // UDP capability remains pinned to the primary route.
}
```

- TCP: select the healthy route with the fewest active streams; rotate ties.
- UDP: use only the primary route so datagram endpoint identity cannot move
  between sessions.
- Existing streams retain their original Mux when routes are added, removed, or
  replaced.
- A failed secondary is removed and replenished without stopping the VPN.
- A primary failure retains the existing reconnect behavior; no unauthenticated
  promotion is allowed.

### Mobile runtime

1. Authenticate one primary carrier before reporting `connected`.
2. Start the packet tunnel immediately from the primary session.
3. Warm one secondary HTTPS session after tunnel startup with bounded jitter.
4. Warm the third session only after load/concurrency crosses the documented
   threshold; close it after a bounded idle interval.
5. Every session independently performs NP/2 authentication, v2.2 extension
   negotiation, key derivation, cell AEAD, replay protection, and Mosaic setup.
6. On network migration, stop pool growth, replace the primary using the current
   migration gate, drain old flows, then rebuild the pool on the new path.
7. `Stop()` cancels all pending dials and closes every pool session promptly.

### Diagnostics

Expose bounded, destination-free fields:

- `carrier_pool_target`
- `carrier_pool_healthy`
- `carrier_pool_assignments`
- `carrier_pool_scale_ups`
- `carrier_pool_failures`

No destination, credential, route token, or per-stream identifier may be logged.

## Threat model

| Threat | Mitigation |
|---|---|
| Session spoofing or pool injection | Every member completes the existing credential proof and server-identity binding before router admission |
| Key/nonce reuse | Each member has an independent handshake and independent directional cell keys/nonces |
| Cross-session reordering | One inner flow is pinned to one member for its complete lifetime |
| Resource exhaustion | Client maximum is three; server per-user and global session limits remain authoritative |
| Connection-count fingerprint | Keep two warm only for performance profiles, jitter secondary startup, create the third only under load, and retire it after idle |
| Secondary failure tears down VPN | Remove only the failed secondary; primary remains authoritative |
| Stop or migration hangs | One runtime cancellation context owns all pending dials and sessions; close remains bounded |

## Tech stack

- Go 1.26.x: configuration, authenticated sessions, TUN routing, mobile runtime.
- Swift/NetworkExtension: profile serialization and bounded diagnostics only.
- Existing `coder/websocket`, HTTP/3, WebRTC, cell AEAD, and Mosaic code; no new
  dependency.

## Project structure

- `internal/config/`: additive profile field and strict validation.
- `internal/tunstack/`: pool-aware flow router.
- `mobile/np2mobile/`: lifecycle, adaptive scaling, migration, diagnostics.
- `clients/ios/`: profile transport and diagnostic presentation.
- `deploy/package/`: server defaults and exported client profiles.
- `docs/reports/`: physical A/B evidence.

## Commands

```sh
C:/Neproto/.tools/go/bin/go.exe test ./... -count=1
ssh mac-89 'cd /Users/intimnyjprysik/.local/share/neproto/ios-stage2 && $HOME/.local/share/neproto/go1.26.5/bin/go test ./internal/tunstack ./mobile/np2mobile'
ssh mac-89 'cd /Users/intimnyjprysik/.local/share/neproto/ios-stage2 && GO_BIN=$HOME/.local/share/neproto/go1.26.5/bin/go ./clients/ios/Scripts/build-frameworks.sh'
docker run --rm -v C:/Neproto:/repo -w /repo ubuntu:24.04 bash deploy/package/tests/install-smoke.sh
```

## Code style

Keep pool selection deterministic and bounded:

```go
route, err := router.selectLeastLoaded()
if err != nil {
    return nil, err
}
return route.open(ctx, metadata)
```

Do not add retry loops that can duplicate target connections after an ambiguous
OPEN result.

## Testing strategy

1. Unit tests first for config bounds, least-loaded selection, tie rotation,
   primary-pinned UDP, secondary removal, and concurrent router updates.
2. Runtime tests with fake authenticated sessions for warmup, cancellation,
   secondary failure, migration, and prompt close.
3. Full Go suite and race suite for session/router/runtime packages.
4. iOS Core tests for optional profile serialization and diagnostic fields.
5. Physical iPhone A/B using identical network, server, workload, and three runs
   per mode. Compare medians and server-side byte counters.
6. Connect/disconnect, LTE/Wi-Fi migration, 15-minute media soak, and server
   resource-limit verification.

## Boundaries

### Always

- Preserve credential authentication, server-identity binding, cell AEAD,
  replay protection, extension negotiation, and Mosaic on every member.
- Keep pool size and all queues bounded.
- Keep UDP pinned to one authenticated primary.
- Provide `max_parallel_carriers=1` rollback.
- Prove improvement on the physical iPhone before enabling the default.

### Ask first

- Increasing the maximum above three.
- Changing the NP/2 wire format or server authentication exchange.
- Mixing HTTPS and HTTP/3 members in one pool.
- Reducing cover overhead or disabling encryption.

### Never

- Share session keys or nonce sequences between carriers.
- Split one ordered inner TCP stream across sessions without an authenticated
  reorder protocol and a new protocol version.
- Log credentials, private routes, or destinations.
- Bypass server per-user/global limits.

## Success criteria

1. All current tests plus pool-specific unit, race, lifecycle, and migration
   tests pass.
2. Existing profiles and single-carrier servers remain compatible.
3. Encryption and Mosaic statistics remain active on every pool member.
4. Stop completes within the existing iOS lifecycle bound with one, two, or
   three members.
5. No secondary failure disconnects a healthy primary VPN.
6. Physical pooled-mode median throughput improves by at least 20 percent over
   single mode on the same route, or the feature remains disabled.
7. Cover plus framing overhead remains inside the configured budget.

## Open questions

- Whether the third carrier should scale on active-flow count, byte rate, or a
  combination; this will be selected by deterministic benchmark evidence.
- Whether HTTP/3 beats HTTPS on the physical LTE route; it remains a separate
  experiment after the HTTPS pool A/B.
