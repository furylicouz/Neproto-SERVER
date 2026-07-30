# NP/2 iOS performance baseline — 2026-07-17

## Executive summary

The reproduced iPhone throughput ceiling is not caused by the VPS having an
insufficient advertised port speed, nor by the iPhone CPU. The same production
NP/2 server delivered 22.58 Mbit/s to the Windows client, while the physical
iPhone's outer NP/2 connection peaked at 8.69 Mbit/s during the captured run.

The primary constraint is the interaction of a 256 KiB per-stream flow-control
window with 111 ms average mobile RTT, RTT spikes, one shared HTTP/1.1
WebSocket/TCP carrier, intentional per-cell cover delay, and up to 30% cover
overhead. The current design is tuned conservatively for correctness and
camouflage rather than bulk throughput.

## Test environment

| Component | Value |
|---|---|
| Date | 2026-07-17 |
| Server | Production NP/2 at `neproto.lyntragram.ru` |
| iPhone | iPhone 12 Pro Max, physical device |
| iOS carrier | HTTPS: TLS 1.3 + WebSocket over HTTP/1.1 |
| NP/2 data plane | Direct NetworkExtension utun → gVisor/tun2socks → NP/2 |
| NP/2 cell encryption | ChaCha20-Poly1305 |
| Initial stream window | 262,144 bytes |
| Cover profile | `web` |
| Cover overhead cap | 30% |
| Web-profile real-cell delay | 0–12 ms |

## Measurements

| Measurement | Result |
|---|---:|
| Windows direct HTTPS, 25 MB | 7,950,607 B/s = 63.60 Mbit/s |
| Windows NP/2 over SOCKS, same 25 MB | 2,822,567 B/s = 22.58 Mbit/s |
| iPhone outer NP/2 peak one-second receive rate | 1,085,630 B/s = 8.69 Mbit/s |
| iPhone outer bytes received during 45 s trace | 12,426,085 bytes |
| iPhone active-sample average outer RTT | 110.93 ms |
| iPhone minimum observed outer RTT | 43 ms |
| iPhone observed RTT spike | 177.16 ms |
| Outer TCP retransmitted bytes | 4,164 bytes |
| PacketTunnel CPU samples | 1,985 running samples / 41.17 s at 1 kHz |
| PacketTunnel CPU estimate | 4.82% of one CPU core |

The 45-second average includes test ramp-up, idle gaps, and upload phases, so it
must not be presented as sustained download throughput. The one-second peak is
the useful reproduction of the reported sub-10-Mbit/s ceiling.

## Findings

| Area | Issue | Impact | Severity | Recommendation |
|---|---|---|---|---|
| Flow control | A 256 KiB window is small for a lossy 100–180 ms mobile path | Sender periodically waits for returned credit | 🟠 High | Benchmark 1, 2, and 4 MiB negotiated windows |
| Cover scheduling | Every real cell receives independent 0–12 ms web-profile jitter | Sequential waits reduce bulk throughput | 🟠 High | Add a bounded bulk/burst scheduler without removing cover semantics |
| Cover bandwidth | Padding and dummy traffic may consume up to 30% | Useful throughput can be materially lower than outer throughput | 🟡 Medium | Report actual per-session overhead and support measured profiles |
| Carrier | All logical streams share one HTTP/1.1 WebSocket/TCP connection | Loss and head-of-line blocking affect every stream | 🟠 High | Design an independent HTTP/2 or HTTP/3 NP/2 carrier |
| Mobile RTT/loss | RTT averaged 111 ms and reached about 177 ms; retransmissions occurred | Window ceiling falls sharply during radio variation | 🟠 High | Size credit from measured bandwidth-delay product and retain hard memory caps |
| PacketTunnel CPU | Only about 4.82% of one core was sampled under the run | CPU is not the current bottleneck | 🟢 Low | Do not optimize crypto or gVisor CPU before network-control changes |
| VPS port rating | A gigabit NIC describes only the server's access link | It does not guarantee end-to-end single-flow throughput | 🟢 Low | Judge the complete client→VPS→target path, not NIC marketing |

## Window analysis

Ignoring all other limits, the per-stream flow-control ceiling is approximately:

```text
throughput ≈ window × 8 / RTT
```

For the current 262,144-byte window:

| RTT | Window-only ceiling |
|---:|---:|
| 110.93 ms | 18.90 Mbit/s |
| 177.16 ms | 11.84 Mbit/s |

NP/2 flow-control credit counts useful DATA payload rather than padding, so the
30% cover allowance must not simply be subtracted from these window ceilings.
Instead, cover traffic consumes outer bandwidth and every web-profile cell also
enters the bounded delay scheduler. The credit-return loop therefore includes
mobile RTT plus DATA and `WINDOW_UPDATE` scheduling/serialization. That
interaction can push the effective useful ceiling below the 11.84 Mbit/s
window-only bound and is consistent with the captured 8.69 Mbit/s peak.

This calculation is a bound, not a claim that the window is the only cause.
Exact attribution still requires the proposed credit-stall and cover-queue
counters; the measured result comes from all constraints acting together.

## Risks

- Increasing the window without bounds increases memory use per stream and per
  session, especially at the configured stream limit.
- Removing cover delay globally would improve benchmarks but weaken the stated
  camouflage behavior and change the protocol contract.
- Increasing padding to look more random can make both throughput and
  fingerprinting worse.
- HTTP/3 cannot be treated as a small transport swap; it needs native datagram,
  loss, migration, and classifier testing.

## Recommendations

1. Add permanent per-session counters for useful bytes, outer bytes, padding,
   dummy bytes, RTT/credit stalls, cell sizes, and queue time without logging
   destinations or payloads.
2. Build an iOS A/B matrix using `quiet` and `web` profiles with 256 KiB,
   1 MiB, 2 MiB, and 4 MiB windows.
3. Coalesce `WINDOW_UPDATE` cells and replenish credit before the receive
   window approaches zero.
4. Change the web cover scheduler from a mandatory independent sleep per cell
   to bounded burst scheduling derived from a measured target distribution.
5. Keep the existing 16 MiB protocol maximum and introduce a lower negotiated
   production cap based on per-session memory budgets.
6. After the flow-control baseline is fixed, evaluate a native HTTP/2 or HTTP/3
   NP/2 carrier and gVisor TCP receive-buffer auto-tuning.

## Action items

| Priority | Task | Status |
|---|---|---|
| P0 | Add reproducible iOS throughput and RTT/credit-stall instrumentation | Pending |
| P0 | Run the cover-profile/window A/B matrix on the physical iPhone | Pending |
| P1 | Specify adaptive window negotiation and memory accounting | Pending |
| P1 | Specify burst-oriented cover scheduling | Pending |
| P1 | Add throughput regression benchmarks and budgets | Pending |
| P2 | Prototype an independent HTTP/2 or HTTP/3 carrier | Pending |

## Verification evidence

- Direct and NP/2 Windows tests used the same 25 MB Cloudflare endpoint.
- The production HTTPS carrier authenticated with the current source before the
  Windows NP/2 measurement.
- iPhone Network trace attached to the running PacketTunnel process and
  identified the outer cellular TCP connection to the pinned production server.
- iPhone Time Profiler attached to the same PacketTunnel process during the
  Cloudflare run.
- No server, firewall, VPN profile, or protocol configuration was modified by
  these measurements.
