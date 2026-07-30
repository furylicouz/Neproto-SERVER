# NP/2 Production Validation — 2026-07-17

━━━━━━━━━━━━━━━━━━━━
📊 PRODUCTION REPORT
━━━━━━━━━━━━━━━━━━━━

## Executive Summary

NP/2 is deployed at `neproto.lyntragram.ru` on the approved VPS. The production binary is `np2-3d1ad2f`. Subsequent source changes through `c472f83` only remove redundant PSK copies from deployment backups; they do not alter the running protocol or carrier code.

Both authenticated carriers work in production. External Windows testing selected WebRTC, opened SOCKS on `127.0.0.1:1080`, and proxied a public HTTP request. Forced HTTPS and WebRTC probes both authenticated successfully. Automatic TLS, the decoy site, restart policy, rollback artifacts, and systemd isolation are active.

This evidence does not establish universal DPI resistance or superiority over Xray. A controlled Xray packet-capture baseline has not yet been measured.

## Findings

| Area | Finding | Status | Evidence |
|---|---|---|---|
| DNS/TLS | Domain resolves to the VPS; public certificate issued automatically | ✅ | External HTTPS request and certificate issuance log |
| Decoy | `/` and unrelated paths return the same normal static site | ✅ | External root and random-path requests |
| WebRTC | ICE/DTLS/SCTP carrier authenticates and carries SOCKS traffic | ✅ | `probe --carrier webrtc`, external SOCKS request |
| HTTPS | TLS/WebSocket carrier authenticates independently | ✅ | `probe --carrier https` |
| Hybrid | `auto` selects a healthy WebRTC carrier; typed-nil fallback panic has a regression test | ✅ | Production probe and Linux race suite |
| UDP bounds | Pion mDNS disabled; active sockets stay within `40000–40100` | ✅ | Live `ss -unap` inventory |
| Service isolation | NP/2 `2.9 OK`, Caddy `3.0 OK` | ✅ | `systemd-analyze security --offline=yes` |
| Existing services | SSH and Zabbix remain active | ✅ | Service and listener inventory |
| Vulnerabilities | No vulnerable called symbols or imported packages | ✅ | `govulncheck v1.6.0`, Go DB dated 2026-07-08 |
| Rollback | Timestamped root-only backups and documented restore procedure | ✅ | Installer output and operations runbook |

## Performance Snapshot

Measurements were taken from the Windows client through the public domain. Setup includes carrier establishment and NP/2 authentication. The sample is useful as a baseline, not a capacity limit.

| Metric | Result |
|---|---:|
| WebRTC setup, 10 samples | p50 1495 ms; p95 1712 ms |
| HTTPS setup, 10 samples | p50 1467 ms; p95 1524 ms |
| SOCKS download, 5 MB | 2.48 MB/s; 19.9 Mbit/s |
| Windows client working set | 17–23 MB during smoke/throughput tests |
| NP/2 server RSS | 18.2 MB with an active WebRTC session |
| Caddy RSS | 54.2 MB |
| Cover overhead ceiling | 30% configured; invariant covered by E2E tests |

## Risks

| Area | Issue | Impact | Severity | Recommendation |
|---|---|---|---|---|
| Comparative claims | No controlled Xray baseline or packet-capture comparison yet | “Better than Xray” cannot be claimed | 🟡 Medium | Run identical targets, networks, loads, and capture summaries |
| Observability | Current signals are systemd/journald, listener inventory, and authenticated `probe`; no RED metrics | Slower diagnosis of aggregate failure/latency changes | 🟡 Medium | Add bounded local metrics without routes, destinations, or key material |
| Firewall | Host UFW remains inactive by deliberate policy | Listener restrictions, not firewall policy, enforce current exposure | 🟡 Medium | Approve and apply an explicit rule set while preserving SSH/Zabbix |
| Key lifecycle | One PSK is accepted at a time | Rotation requires a short reconnect window | 🟡 Medium | Add overlapping key IDs before multi-user rollout |
| Client operations | Windows client is manually started, not installed as a service | It will not survive reboot automatically | 🟢 Low | Package a restricted Windows service or scheduled task |
| Scope | MVP proxies SOCKS TCP only; no UDP proxy or TUN | Some applications are unsupported | 🟢 Low | Add only after protocol and carrier telemetry is stable |
| Security assurance | No independent external audit | Unknown implementation risks may remain | 🟡 Medium | Commission review before broad/public use |

## Recommendations

1. Treat this deployment as a private canary.
2. Add aggregate carrier/auth/session/error histograms before adding users.
3. Run a reproducible NP/2 versus selected Xray baseline; report packet shape, availability, p50/p95, throughput, CPU, memory, and overhead.
4. Keep secrets and random routes out of logs, URLs shared in chat, repositories, and monitoring labels.
5. Use the checked-in installer for additional servers so every node receives a separate domain, routes, PSK, and limits.

## Action Items

| Priority | Task | Status |
|---|---|---|
| P0 | Production TLS, WebRTC, HTTPS, SOCKS, decoy, and restart verification | ✅ Done |
| P0 | Race, vet, E2E, deployment validation, and vulnerability scan | ✅ Done |
| P1 | Aggregate observability and health endpoint | ⏳ Pending |
| P1 | Xray comparative packet capture and benchmark | ⏳ Pending |
| P1 | Explicit firewall approval and rule deployment | ⏳ Pending |
| P2 | Windows autostart/service packaging | ⏳ Pending |
| P2 | Multi-key rotation/control plane | ⏳ Pending |

## Reproduction

```text
neproto-client check --config client.json
neproto-client probe --config client.json --carrier auto
neproto-client probe --config client.json --carrier webrtc
neproto-client probe --config client.json --carrier https
go test ./...
go vet ./...
go test -race ./...
govulncheck ./...
```

Operational procedures are in `docs/OPERATIONS.md`. Caddy behavior follows the official [`handle`](https://caddyserver.com/docs/caddyfile/directives/handle), [`reverse_proxy`](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy), and [server protocol options](https://caddyserver.com/docs/caddyfile/options#protocols) documentation. Vulnerability scanning follows the official [Go govulncheck guidance](https://go.dev/doc/tutorial/govulncheck).
