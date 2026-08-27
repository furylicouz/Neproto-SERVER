# NP/2 iOS HTTPS-only A/B Candidate

Status: approved diagnostic candidate

Date: 2026-08-27

## Purpose

The candidate isolates the iOS packet data plane from the mobile UDP/QUIC
route. It keeps the same Darwin utun adapter, userspace TCP/IP stack, NP/2
authentication, cell AEAD, flow control, Mosaic cover, server, credential and
destination policy. Only the outer carrier changes from HTTP/3 WebTransport to
HTTPS WebSocket over TLS 1.3 and TCP.

This is a single-transport experiment, not a compatibility fallback. The
existing `http3-only` implementation remains available as the baseline.

## Public client contract

The additive carrier policy value is `https-only`.

An `https-only` mobile configuration MUST contain:

- exactly one `wss://` `https_url` whose host equals `server_identity`;
- one bounded `https_timeout`;
- `max_parallel_carriers` equal to `1`;
- no HTTP/3 or WebRTC endpoint or timeout;
- `require_datagrams` equal to `false`.

The strict connector MUST construct only the HTTPS WebSocket carrier. It MUST
not retain or invoke HTTP/3 or WebRTC dialers. TLS certificate and hostname
verification remain mandatory. NP/2 authentication MUST complete before the
Packet Tunnel installs its default route.

The saved `NETunnelProviderProtocol` MUST use the iOS full-tunnel policy:
`includeAllNetworks` is `true` and `enforceRoutes` is `false`. The protocol
`serverAddress` MUST use the validated concrete NP/2 server address when the
profile supplies one so iOS can automatically keep the carrier endpoint off
the tunnel. `enforceRoutes` MUST NOT be combined with the default included
routes: iOS 26 can drop all included traffic for that configuration instead of
delivering it to the Packet Tunnel.

The first candidate remains a single authenticated session. Imported
Constellation metadata may remain stored, but the strict runtime does not
advertise Constellation until its control exchange is implemented. Forward
secrecy remains independent and enabled when requested.

## Server boundary

No server protocol or wire-format change is required. A server with the
existing private HTTPS WebSocket route can accept both the HTTP/3-only baseline
and this HTTPS-only candidate. The NP/2 backend remains non-public and TLS is
terminated only by the configured production ingress.

## Diagnostics

The iOS diagnostics surface MUST report:

- policy `https-only`;
- carrier `HTTPS_WEBSOCKET` while connected;
- the same application upload/download counters used by the HTTP/3 candidate;
- no QUIC health event for an HTTPS session.

Diagnostics must not contain credentials, targets, DNS names, payloads or
carrier route addresses.

## Acceptance test

On the same physical iPhone, access network, server, speed-test endpoint and
test interval:

1. Record three HTTP/3-only download/upload results and their median.
2. Install this HTTPS-only candidate and confirm its policy and carrier in the
   diagnostics screen.
3. Record three HTTPS-only results and their median.
4. Keep the server and NP/2 profile unchanged between the two samples.

If HTTPS-only removes the 3-4 Mbit/s download ceiling, the limiting component
is the UDP/QUIC path or its network treatment, not the shared iOS utun data
plane. If the ceiling remains, the next gate is an on-device CPU and packet-loop
profile of the shared iOS data plane.
