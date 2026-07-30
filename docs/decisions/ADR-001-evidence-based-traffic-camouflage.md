# ADR-001: Evidence-based traffic camouflage claims

## Status

Accepted

## Date

2026-07-17

## Context

NP/2 has an original authenticated handshake, session-specific cell mapping,
multiplexing, bounded cover scheduling, and mandatory per-cell
ChaCha20-Poly1305. It is not wire-compatible with VLESS, Trojan, SOCKS,
Shadowsocks, or Hysteria.

Wire-format uniqueness is not the same as resistance to traffic analysis. A
passive observer can still classify the outer carrier using its TLS ClientHello,
SNI, ALPN, connection lifetime, packet sizes, burst timing, and server/IP
reputation. An active observer can also probe public endpoints and compare their
responses.

The current iOS production path is TLS 1.3 plus WebSocket over HTTP/1.1 on the
project's own domain. Caddy serves a normal decoy site on unrelated paths, while
NP/2 uses a private high-entropy route. The route, NP/2 cells, destinations, and
payloads are hidden by TLS and the mandatory inner AEAD, but SNI and the outer
TLS/WebSocket behavior remain observable.

Modern Xray deployments are not represented accurately by comparing NP/2 only
with bare VLESS. Current Xray supports REALITY, uTLS/Browser Dialer, XHTTP,
XTLS Vision, and VLESS Encryption. Xray's own documentation warns that
WebSocket has significant traffic characteristics, including HTTP/1.1 ALPN,
and recommends XHTTP for this reason.

## Decision

NP/2 documentation and product claims must distinguish the following:

1. **Protocol independence:** NP/2 is an original wire protocol and does not
   expose a standard VLESS header or VLESS authentication exchange.
2. **Content confidentiality:** authenticated NP/2 cells are encrypted inside
   an independently authenticated TLS/DTLS carrier.
3. **Traffic camouflage:** the current carriers reduce simple signatures but
   are not proven indistinguishable from ordinary browser, game, or video-call
   traffic.
4. **Comparative strength:** NP/2 may resist rules that target known VLESS
   endpoints or wire formats, but it is not currently proven stronger than a
   well-configured VLESS + REALITY + XHTTP/Vision deployment.

No claim that NP/2 is "invisible", "unrecognizable", or categorically "better
than VLESS" may be made without reproducible comparative evidence.

## Required evidence for stronger claims

A stronger camouflage claim requires all of the following under identical
workloads and network conditions:

- packet captures for NP/2, ordinary Safari traffic, and selected Xray
  baselines;
- TLS/JA4, SNI, ALPN, HTTP-version, packet-size, burst, timing, and session
  lifetime comparisons;
- passive-classifier and active-probe regression tests;
- availability, latency, throughput, CPU, memory, and cover-overhead results;
- independent protocol and cryptographic review.

Random padding alone is not accepted as evidence. Padding and timing must follow
a measured target distribution and stay within explicit latency and bandwidth
budgets.

## Consequences

- NP/2 keeps its independent handshake, cell protocol, and inner encryption.
- The decoy site and private carrier routes remain useful defenses against
  unsophisticated scanning, but are not treated as a REALITY equivalent.
- HTTPS/WebSocket remains a compatibility carrier, not the final camouflage
  target.
- Future carrier work should prioritize a native NP/2 HTTP/2 or HTTP/3
  request/response state machine, browser-accurate TLS behavior, active-probe
  handling, and measured classifier resistance.
- Uniqueness is treated as a possible fingerprint once an observer obtains
  samples; a larger anonymity set and realistic behavior matter more than
  novelty alone.

## Alternatives considered

### Claim superiority from protocol novelty

Rejected. Encryption hides the inner format from passive observers, while the
outer carrier remains classifiable. A unique stable fingerprint may become
easier to identify after training.

### Add unrestricted random bytes

Rejected. Bytes outside a standards-compliant carrier create protocol
anomalies, and unmodelled random padding can become its own signature.

### Copy VLESS/REALITY wire behavior

Rejected. NP/2 remains an independent protocol. It may adopt validated design
principles, but not VLESS compatibility or an undocumented imitation.

## References

- Xray VLESS: https://xtls.github.io/en/config/inbounds/vless.html
- Xray REALITY: https://xtls.github.io/en/config/transports/reality.html
- Xray TLS/uTLS: https://xtls.github.io/en/config/transports/tls.html
- Xray WebSocket warning: https://xtls.github.io/en/config/transports/websocket.html
- Xray XHTTP: https://xtls.github.io/en/config/transports/xhttp.html
