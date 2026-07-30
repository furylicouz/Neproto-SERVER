# ADR-002: WebTransport over HTTP/3 as the NP/2 fast carrier

Status: accepted for implementation on 2026-07-18

## Context

NP/2 v2.2 requires a real UDP-capable carrier with a reliable byte stream and
unreliable datagrams. HTTP Datagrams cannot be attached to an arbitrary
ordinary GET or POST request: the application protocol carried by HTTP/3 must
define their semantics. Terminating HTTP/3 in Caddy would also prevent the
loopback HTTP/1 backend from receiving those datagrams.

The carrier must therefore use a standardized HTTP/3 extension end to end,
must not emit fake game handshakes, and must retain the authenticated NP/2
inner protocol and all existing destination-policy controls.

## Decision

NP/2 uses WebTransport Extended CONNECT over HTTP/3 for `CarrierHTTP3`.

- The public outer protocol is genuine TLS 1.3, QUIC, HTTP/3 and WebTransport.
- One client-opened bidirectional WebTransport stream carries the existing
  reliable NP/2 `Carrier` messages. A one-byte zero carrier preface activates
  the lazily opened QUIC stream, followed by four-byte big-endian lengths.
- WebTransport datagrams implement the optional `DatagramCarrier` contract.
- NP/2 challenge/response authentication is the first reliable application
  exchange. No target socket is opened by the carrier.
- No custom WebTransport application-protocol token is advertised. The private
  high-entropy route and inner authentication select NP/2 without adding a
  stable optional header.
- 0-RTT application data is not used.
- TLS certificate and hostname verification are mandatory. Tests may provide a
  private root CA; `InsecureSkipVerify` is rejected by production constructors.
- The server uses the safe same-origin WebTransport default. Native clients
  send no `Origin`; browser requests with a foreign origin are rejected.

## Listener and certificate ownership

Caddy owns TCP ports 80 and 443 and is explicitly limited to HTTP/1.1 and
HTTP/2. NeProto owns UDP port 443 for HTTP/3. Both use the same domain and a
certificate valid for that domain.

The deployment manager provisions explicit certificate and key paths readable
by the two unprivileged services. Certificate rotation is atomic: validate the
new pair, replace both files, reload Caddy, then restart the UDP listener. A
failed validation leaves the previous pair active. The H3 feature flag remains
off when either file is absent or unreadable.

## Resource and abuse limits

- Maximum reliable carrier message: the existing NP/2 carrier message maximum.
- Maximum accepted WebTransport sessions: `max_http3_sessions`, never greater
  than the global session cap.
- Exactly one reliable bidirectional stream is accepted per session; extra
  streams are reset.
- Handshake, first-stream, idle and shutdown deadlines are finite.
- Incoming datagrams are capped before copying into application queues.
- Reliable and datagram receive queues are bounded. Overflow terminates the
  affected unauthenticated carrier or drops an authenticated UDP datagram as
  defined by the NP/2 fast-path layer; it never grows memory without bound.
- QUIC keepalive is disabled unless a later measured mobile-network experiment
  justifies a bounded setting.

## Rollout and kill switch

HTTP/3 is additive. A server or profile with H3 disabled behaves as v2.1/v2.2
HTTPS+WebRTC. Client auto mode races H3 and WebRTC only when an H3 URL is
present, and always retains HTTPS fallback. `enable_http3=false` is the server
kill switch; removing `http3_url` is the client kill switch.

## Consequences

This produces traffic that is truthfully HTTP/3/WebTransport and is suitable
for low-latency game or media-style workloads. It does not claim to impersonate
a named game, guarantee DPI invisibility, or make every flow statistically
identical to browser traffic. WebTransport is still evolving, so the dependency
is pinned and protocol compatibility is covered by integration tests and the
server-first rollout.

## References

- WebTransport server and origin validation: https://quic-go.net/docs/webtransport/server/
- WebTransport client and datagram requirement: https://quic-go.net/docs/webtransport/client/
- WebTransport sessions: https://quic-go.net/docs/webtransport/session/
- HTTP Datagrams: https://www.rfc-editor.org/rfc/rfc9297.html
- QUIC Datagrams: https://www.rfc-editor.org/rfc/rfc9221.html
- HTTP/3: https://www.rfc-editor.org/rfc/rfc9114.html
