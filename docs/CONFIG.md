# NP/2 Configuration Contract

NP/2 uses strict JSON. Unknown fields, duplicate top-level JSON values, insecure URLs, public local binds, malformed durations, unsafe UDP ranges, and invalid secret files fail before any listener or outbound connection is created.

## Secret file

The secret file contains exactly one unpadded base64url value encoding 32 random bytes, optionally followed by one newline. On Unix it must be a regular, non-symlink file with no group or other permission bits; mode `0600` is recommended.

Generate a value with `neproto-server generate-secret`. Never place the value in JSON, command arguments, logs, repository files, or generated links.

## Client

```json
{
  "server_identity": "vpn.example.com",
  "secret_file": "/etc/neproto/client.secret",
  "socks_listen": "127.0.0.1:1080",
  "https_url": "wss://vpn.example.com/<private-https-route>",
  "webrtc_signaling_url": "https://vpn.example.com/<private-webrtc-route>",
  "http3_url": "https://vpn.example.com/<private-http3-route>",
  "profile": "interactive",
  "carrier_policy": "performance",
	"cover_mode": "off",
  "max_cover_overhead_percent": 30,
  "initial_window_bytes": 2097152,
  "max_streams": 128,
  "max_parallel_carriers": 3,
  "max_socks_connections": 128,
  "webrtc_timeout": "5s",
  "https_timeout": "10s",
  "http3_timeout": "5s",
  "require_datagrams": false,
  "carrier_cache_ttl": "10m"
}
```

`socks_listen` must resolve syntactically to a loopback IP. The WSS, HTTPS,
and HTTP/3 URL hostnames must equal `server_identity`. All three private route
paths are distinct and at least 16 characters long. `http3_url` and
`http3_timeout` may both be omitted for a v2.1-compatible profile.
`require_datagrams=true` rejects a session unless reliable UDP and an
unreliable fast path are both authenticated and negotiated; keep it false
during a rolling server upgrade. Profiles are `quiet`, `web`, and
`interactive`; overhead is an exact value from 0 through 100.

`carrier_policy` defaults to `performance`, which tries HTTPS first and keeps
HTTP/3 and WebRTC as authenticated fallbacks. Set it to `udp-first` only after
measuring that the current UDP route outperforms HTTPS; the UDP-first policy
uses the cached authenticated carrier race.

`max_parallel_carriers` is bounded to `1..3`. Desktop, `quiet`, and
`udp-first` profiles default to one. The direct iOS `performance` client
defaults to adaptive mode automatically: two independently authenticated HTTPS
carriers are kept warm, a third is added only under load, and it is retired
after idle. A value of `1` is the wire-compatible rollback and does not weaken
or change NP/2 encryption. One inner TCP flow always remains on one carrier;
UDP stays pinned to the primary carrier.

### Cover mode and Mosaic behavior

`cover_mode` defaults to `off`. In this performance mode the mandatory NP/2
authentication, session-specific type map, cell AEAD, flow control, and
multiplexing remain active, while padding, dummy cells, cover delays, and
Mosaic classification are bypassed. Set `cover_mode` to `mosaic` explicitly on
both peers only for a measured cover experiment. There is deliberately no
fourth `mosaic` configuration profile. Mosaic peers advertise
`CapabilityMosaicCover` after authentication and activate it only when both
sides select the capability:

| Configured profile | Legacy peer | Mosaic-capable peer |
|---|---|---|
| `quiet` | fixed quiet | fixed quiet |
| `web` | fixed web | starts web; may select realtime or stream locally |
| `interactive` | fixed interactive | starts realtime; may select web or stream locally |

`max_cover_overhead_percent` is ignored in `off` mode and remains the global
ceiling across all Mosaic transitions.
The `stream` class is an internal zero-delay fast path, not a value accepted in
JSON or an assertion that the outer carrier is HTTP/3. The normative state
machine is in [`NP2-MOSAIC-SPEC.md`](NP2-MOSAIC-SPEC.md).

## Server

```json
{
  "server_identity": "vpn.example.com",
  "secret_file": "/etc/neproto/server.secret",
  "listen": "127.0.0.1:9080",
  "metrics_listen": "127.0.0.1:9464",
  "https_path": "/<private-https-route>",
  "webrtc_path": "/<private-webrtc-route>",
  "enable_http3": true,
  "enable_webrtc_datagrams": true,
  "enable_http3_datagrams": true,
  "http3_listen": ":443",
  "http3_path": "/<private-http3-route>",
  "http3_cert_file": "/etc/neproto/tls/fullchain.pem",
  "http3_key_file": "/etc/neproto/tls/privkey.pem",
  "udp_port_min": 40000,
  "udp_port_max": 40100,
	"cover_mode": "off",
  "max_cover_overhead_percent": 30,
  "initial_window_bytes": 262144,
  "max_streams": 128,
  "max_sessions": 32,
  "max_webrtc_peers": 32,
  "max_http3_sessions": 32,
  "max_target_connections": 128,
  "resource_limits": {
    "max_sessions_per_user": 8,
    "max_tcp_connections_global": 6000,
    "max_tcp_connections_per_user": 512,
    "max_udp_associations_global": 10000,
    "max_udp_associations_per_user": 1024,
    "udp_packets_per_second_global": 100000,
    "udp_packets_per_second_per_user": 20000,
    "udp_bytes_per_second_global": 268435456,
    "udp_bytes_per_second_per_user": 67108864,
    "dns_queries_per_second_global": 5000,
    "dns_queries_per_second_per_user": 500,
    "target_creates_per_second_global": 20000,
    "target_creates_per_second_per_user": 2000
  },
  "dial_timeout": "10s",
  "gather_timeout": "8s",
  "connect_timeout": "12s",
  "http3_handshake_timeout": "5s",
  "http3_idle_timeout": "45s",
  "shutdown_timeout": "10s"
}
```

The backend and optional Prometheus metrics listeners are loopback-only and
must use different ports. `metrics_listen` may be omitted to disable scraping.
The endpoint is `GET /metrics`; it exposes bounded aggregate carrier, session,
stream, UDP, error-category, goroutine, resident-memory, and descriptor
counters without credentials, destinations, private paths, or payloads.
Caddy owns TCP 443 for HTTP/1.1 and HTTP/2; NeProto owns UDP 443
for WebTransport over HTTP/3. Both read the same TLS certificate. The WebRTC
port range is explicit and limited to at most 1000 ports. HTTP/3 and each
unreliable datagram path have independent kill switches. Private, loopback,
link-local, multicast, unspecified, documentation, benchmarking, and other
special target addresses remain blocked; there is no production config switch
that bypasses that policy.

`max_sessions` is the process-wide pre-authentication/session ceiling;
`resource_limits.max_sessions_per_user` is applied only after a credential has
authenticated. TCP and UDP quotas are enforced both per session and across the
process/per authenticated user. UDP packet, byte, port-53 DNS, and new-target
rates use one-second token buckets and are consumed atomically. A first reply
from each UDP target is additionally limited to three times authenticated
client bytes plus a 1280-byte path-MTU allowance. Omitted `resource_limits`
fields receive the documented production defaults; an invalid per-user/global
relationship fails `check` before listeners are opened.

### Desktop SOCKS5 UDP

The desktop listener implements RFC 1928 `UDP ASSOCIATE` on a loopback,
ephemeral UDP relay owned by the TCP control connection. It accepts IPv4,
IPv6, and domain targets, rejects fragmented SOCKS UDP records, pins the relay
to the authenticated local client endpoint, and closes the association when
the control connection closes. DNS names in TCP requests continue to resolve
on the NP/2 server when the application uses SOCKS remote-DNS mode (commonly
shown as `socks5h`). iOS does not use this adapter; it sends utun TCP/UDP
directly through NP/2.

## Commands

```text
neproto-client check --config <path>
neproto-client probe --config <path> [--carrier auto|http3|webrtc|https]
neproto-client run --config <path>
neproto-client version

neproto-server check --config <path>
neproto-server run --config <path>
neproto-server generate-secret
neproto-server version
```

`check` performs parsing, normalization, security validation, and secret-file validation without opening listeners or connecting to a network.

`probe` opens and authenticates one bounded NP/2 session without starting
SOCKS. `auto` races complete authenticated candidates with bounded staggering;
`http3`, `webrtc`, and `https` force one carrier for deployment diagnostics.
The second output line reports the negotiated cover mode without exposing a
route, target, credential, or packet content:

```text
carrier=https fallback=false authentication=ok
cover=mosaic class=web transitions=0
```

A legacy peer reports `cover=fixed class=fixed-web` (or the configured fixed
profile). A successful probe proves negotiation and initial state only; it does
not generate enough workload to validate realtime/stream classification.
