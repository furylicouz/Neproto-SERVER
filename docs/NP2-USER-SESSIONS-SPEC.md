# NP/2 user sessions, traffic accounting and device policy

Status: implementation contract

## 1. Purpose

The server and administration UI expose whether a user is online, application
payload traffic totals, last activity, enrolled devices and a configurable
device ceiling. The implementation never logs destinations, payloads, root
secrets, hardware identifiers or public client IP addresses.

## 2. Identity model

- A user is the authenticated credential ID already selected by the NP/2
  challenge-response handshake.
- A device is a random 128-bit `device_id` created once per application
  installation. It is not derived from IP address, IMEI, IDFA, IDFV or other
  hardware/platform identifiers.
- `device_id` is present only when `FeatureDeviceIdentity` is negotiated and is
  covered by the response transcript HMAC. A captured response cannot move the
  identifier to another session.
- Multiple HTTPS, HTTP/3 or WebRTC carrier sessions with the same credential and
  `device_id` count as one online device.
- IP addresses are not device identities: mobile IPs change, carrier-grade NAT
  shares one IP between customers and one device may use Wi-Fi and LTE.
- The identifier is not DRM. A party that owns the credential and application
  state can copy it. The policy prevents ordinary credential sharing and bounds
  enrolled installations; it does not claim resistance to a modified client.

## 3. Compatibility and enforcement

- Servers advertise `FeatureDeviceIdentity`; clients request it only when they
  have a valid non-zero 16-byte identifier. A device-aware client omits this
  optional feature when a legacy server challenge does not advertise it.
- Legacy response framing remains accepted when the feature is not selected.
- `max_devices = 0` means unlimited and permits legacy clients.
- Unlimited mode never rejects a session because diagnostic device history is
  full; after the bounded 64-device history is reached, additional identities
  are admitted as unlabelled sessions until an offline entry is removed.
- `max_devices > 0` is fail-closed: a session without a negotiated device ID is
  rejected before any target is dialled.
- A known device may reconnect. An unknown device is enrolled atomically only
  while the user's bounded device ceiling has remaining capacity.
- Revoking a user prevents new sessions. Deleting a revoked user also removes
  its accounting and enrolled-device state.

## 4. Accounting semantics

- `online` means at least one authenticated, admitted carrier session is active.
- `online_devices` is the number of distinct admitted device IDs with active
  sessions. `active_sessions` remains a separate carrier count.
- `upload_bytes` is useful NP/2 payload accepted from client to server.
- `download_bytes` is useful NP/2 payload sent from server to client.
- Padding, dummy cover bytes, TLS/DTLS/QUIC/IP overhead and destination labels
  are excluded.
- Active sessions are sampled and persisted at a bounded interval; normal close
  performs a final sample. A crash may lose at most one interval of counters.
- Counters are unsigned 64-bit saturating values. State is bounded by the
  configured maximum credentials and devices.
- Reset increments a per-user generation in the administrator-owned policy
  file. The runtime atomically adopts the generation, zeros displayed totals
  and establishes new baselines for still-active sessions.

## 5. Storage and permissions

- `/etc/neproto/users/index.json` is administrator-owned policy state, readable
  by the NP/2 service group and never writable by the network service.
- `/var/lib/neproto/usage/state.json` is runtime-owned accounting state. Writes
  use a temporary regular file, bounded JSON, fsync and atomic replacement.
- Symlinks, devices, oversized files, duplicate users/devices, invalid IDs,
  future versions and trailing JSON are rejected.
- Web administration accesses the state through the authenticated local control
  API. The public Next.js process never receives filesystem permissions for the
  usage directory.

## 6. Administrative API

Existing user objects are extended additively with:

```text
online, last_seen, active_sessions, online_devices,
enrolled_devices, max_devices, upload_bytes, download_bytes, total_bytes
```

Mutations:

- `PATCH /v1/users/{id}/policy` with `{ "max_devices": 0..16 }`.
- `POST /v1/users/{id}/traffic-reset` with an empty JSON object.
- `DELETE /v1/users/{id}/devices/{device_id}` removes an offline enrolled
  device. Removing an online device returns conflict.

All mutations require the existing authenticated, same-origin web-admin path,
validate bounded input, serialize with other user mutations and return stable
error codes without raw internal errors.

## 7. Cluster boundary

The current release accounts for sessions authenticated by a standalone server
or directly by the cluster master. It does not present master-local counters as
cluster-wide counters. Cluster edge nodes keep accepting their synchronized
credentials without applying the master's local policy file; this prevents a
partial rollout from rejecting internal peer or edge-user sessions.

Cluster-wide accounting and device enforcement require authenticated policy
replication plus signed, bounded node snapshots keyed by `(node_id, user_id)`.
Duplicate reports must replace the prior snapshot for that node rather than
double-counting. Until this replication contract is implemented, the web UI
labels these values as current-node data and must not claim a global limit.

## 8. Verification gates

- Golden legacy and device-aware authentication vectors.
- Rejection of zero, truncated, trailing and unauthenticated device IDs.
- Concurrent admission proves three carrier sessions for one device consume one
  device slot and a second installation is rejected at a limit of one.
- Online/offline and final byte samples, crash-window persistence, reset during
  an active session, saturation and corrupt-state tests.
- Admin API authorization, validation, conflict and additive response tests.
- iOS configuration tests prove one installation ID is reused across profiles
  and never uses platform/hardware identifiers.
- Go race tests, Swift tests, production frontend build, browser interaction and
  isolated bare-metal/Docker installation lifecycle.
