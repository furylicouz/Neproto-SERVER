# NP/2 versus VLESS Comparative Lab

## Status

Approved for an isolated desktop/VPS experiment. The production NP/2 listener
on TCP/UDP 443 must not be modified by the lab.

## Objective

Produce reproducible evidence for a scoped statement about NP/2 Constellation
relative to two current Xray baselines:

1. VLESS + REALITY + XTLS Vision for the low-overhead TCP baseline.
2. VLESS + XHTTP + REALITY for the HTTP-shaped baseline.

The lab measures performance, failure recovery, externally observable metadata,
and active-probe behavior. It does not claim universal DPI or GFW resistance.

## Experiment boundaries

- Use the same client machine, VPS, target object, run window, and number of
  repetitions for every candidate.
- Run the direct route before and after every randomized candidate block.
- Pin one IP address family for every candidate block. IPv4 and IPv6 results
  are separate experiments.
- When the target uses DNS load balancing, pin the same resolved target IP for
  direct, NP/2, and Xray blocks while retaining the original hostname for TLS.
- Keep the production NP/2 service and Caddy configuration unchanged.
- Bind experimental server listeners to isolated high ports or loopback
  reverse-proxy routes and remove them after the experiment.
- Never write credentials, private paths, destinations, payload bytes, full
  packet contents, or raw authentication errors to lab artifacts.
- Pin every binary and container by version and record its digest.
- A physical iPhone is an optional additional network vantage point, not a
  prerequisite for the desktop/VPS stage.

## Sample schema

Every request emits one JSON object using schema
`np2-comparative-sample/v1` with the following bounded fields:

| Field | Meaning |
|---|---|
| `timestamp` | UTC start time |
| `run_id` | Identifier shared by one experiment |
| `implementation` | `direct`, `np2`, or `vless` |
| `profile` | Versioned profile label, never a secret route |
| `transport` | `direct`, `https`, `http3`, `webrtc`, `vision`, or `xhttp` |
| `network` | Coarse test network label |
| `endpoint` | Non-secret target label |
| `iteration` | One-based repetition number |
| `success` | Whether the expected response completed |
| `http_status` | HTTP status, when available |
| `bytes` | Useful response bytes |
| `connect_ms` | Connection establishment time |
| `ttfb_ms` | Time to first response byte |
| `total_ms` | Complete request time |
| `throughput_bps` | Useful bits divided by total request time |
| `error_category` | Stable category only; no raw error text |

The summarizer uses successful samples for latency and throughput percentiles,
but includes failures in the success rate. Percentiles use nearest-rank
selection. Relative throughput is the candidate median divided by the matched
direct median for the same network and endpoint.

## Phase A: performance

For each candidate, run at least 20 cold requests and 20 warm requests against
the same incompressible object. Randomize candidate order. Report:

- success count and rate;
- useful throughput p50/p95;
- connect, TTFB, and total latency p50/p95;
- ratio to the direct route;
- client/server CPU, RSS, and outer/useful byte ratio.

## Phase B: controlled impairment

On an isolated Linux network namespace apply, separately:

- RTT 20, 80, and 150 ms;
- packet loss 0, 1, 3, and 5 percent;
- bounded jitter and reordering;
- UDP/443 rejection;
- TCP reset and one-carrier loss.

Measure completion rate, useful throughput, reconnect time, and whether an
already-open logical flow continues without duplicate or missing bytes.

## Phase C: passive metadata

Capture headers only and extract metadata using a pinned tshark/Zeek toolchain.
Sanitized derived artifacts may contain direction, timestamp delta, packet and
TLS-record sizes, IP protocol, TCP flags, SNI class, ALPN, TLS/QUIC version,
connection lifetime, and burst boundaries. Raw captures remain ephemeral.

Training and validation must be separated by day or network. A classifier may
not use IP addresses, ports, secret paths, DNS names, or run identifiers. Report
ROC-AUC plus true-positive rate at 0.1 percent and 1 percent false-positive
rates. A result from one route is evidence for that route only.

## Phase D: active probes

Send bounded unauthenticated HTTP, WebSocket, TLS, QUIC, replay, truncation, and
malformed-extension probes. Verify that no target socket is allocated before
authentication and compare status, length bucket, and response timing with the
decoy/control service.

## Phase E: censored-network validation

A conclusion about TSPU, GFW, or another deployed DPI requires authorized test
clients inside multiple affected access networks. Run randomized NP/2 and Xray
blocks over several days and report availability and time-to-first-byte. Server
tests alone cannot satisfy this gate.

## Decision rules

NP/2 may be described as better for a named tested network only when:

1. its completion rate is higher with a confidence interval that does not
   overlap the selected VLESS baseline;
2. its median useful throughput remains within the declared product budget;
3. passive classification is no easier at the selected false-positive rate;
4. active probes do not expose an NP/2-specific response;
5. results reproduce on a held-out day or network.

Failure to meet a camouflage gate blocks comparative marketing claims, not the
normal NP/2 build or user operation.
