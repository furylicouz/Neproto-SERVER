# Neproto Chameleon

## Problem Statement

How might we build a personal proxy protocol that has no VLESS-compatible wire format, uses real cover protocols rather than malformed imitation, changes observable traffic shape within a controlled budget, and remains usable when either UDP or TCP is unavailable?

## Recommended Direction

Build a carrier-independent Chameleon Core with its own server-challenge authentication, multiplexed streams, session-specific cell mapping, flow control, and adaptive cover scheduler. Run it primarily over a standards-compliant WebRTC DataChannel and fall back to ordinary HTTPS/WebSocket.

This is preferable to appending random bytes or imitating a named game. Random bytes outside a carrier create anomalies; a named-game imitation requires the complete proprietary handshake and state machine. WebRTC provides a real interactive UDP state machine, while behavioral cover profiles shape sizes, bursts, and timing without making a false protocol identity claim.

## Key Assumptions to Validate

- [ ] WebRTC/UDP is reachable on the user's typical networks. Test from the actual client network and device.
- [ ] A 30% maximum cover budget is acceptable. Measure real byte overhead and latency.
- [ ] Hybrid fallback improves availability without unacceptable connection delay. Force-block UDP and measure time to first proxied byte.
- [ ] Adaptive profiles reduce stable trace features compared with `quiet` mode and a selected Xray baseline. Compare packet captures and summary distributions.
- [ ] The 2-vCPU/2-GiB VPS can sustain the intended concurrency. Benchmark before optimization.

## MVP Scope

- Windows/Linux command-line client, Ubuntu server, loopback SOCKS5 TCP adapter.
- One persistent multiplexed NP/2 session.
- WebRTC DataChannel primary carrier and HTTPS/WebSocket fallback.
- Challenge-response authentication, bounded replay/unauthenticated state, destination policy.
- `quiet`, `web`, and `interactive` cover profiles with a hard overhead budget.
- Caddy decoy site, systemd service, end-to-end and comparative trace tests.

## Not Doing (and Why)

- Named commercial game impersonation: incorrect state behavior is an active-probe signature.
- Custom encryption: standard TLS/DTLS already supplies reviewed confidentiality and integrity.
- Mobile UI in MVP: it does not validate the difficult protocol assumptions.
- UDP proxy payloads in MVP: first prove the carrier and stream protocol correctly.
- Universal "better than Xray" claim: performance and resistance depend on configuration, network, and classifier.

## Open Questions

- Exact production domain.
- First real client network used for UDP/fallback testing.
- Whether Android becomes the next phase immediately after the Windows/Linux MVP.
