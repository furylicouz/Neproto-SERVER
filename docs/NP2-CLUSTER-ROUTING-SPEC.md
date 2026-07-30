# NP/2 Constellation Cluster and Routing Specification

Status: release candidate; two-VPS and physical-device gates pending

Date: 2026-07-19

## 1. Objective

Extend NP/2 Constellation with a production cluster control plane. An existing
NeProto installation becomes the authoritative master. An administrator can
enrol data-plane nodes over SSH, operate them from `np`, publish a restricted
server catalogue to users, and route selected traffic through a chosen egress
node or a bounded chain of nodes.

The feature is additive. A server or client without the cluster capability
continues to use its existing single-server NP/2 v2.2/Constellation behaviour.

## 2. Roles and limits

| Role | Responsibility |
|---|---|
| master | Authoritative cluster state, user access policy, catalogue signing, health aggregation and ingress routing |
| ingress | Accepts an authorised client session and evaluates mandatory routes |
| relay | Carries an authenticated NP/2 inter-node hop |
| egress | Opens the final destination connection after policy validation |
| client | Verifies and caches a signed catalogue, selects an allowed server and applies the effective route table |

Version 1 supports exactly one master, at most 32 nodes, 512 routes, 10,000
users, and three inter-node hops. Multi-master consensus and transparent import
of third-party proxy protocols are outside this version.

## 3. Trust model

1. The master owns an Ed25519 catalogue signing key. Its private half is stored
   in a mode-0600 file and never copied to a node or client.
2. The initial QR/profile pins the catalogue public key and cluster identifier.
3. Every published catalogue contains a monotonically increasing revision,
   issue time and expiry time. Clients reject invalid signatures, revision
   rollback, expired catalogues and a different cluster identifier.
4. Each node receives a unique 256-bit inter-node credential. Revoking or
   removing a node invalidates that credential and drains its sessions.
5. User access is enforced by ingress and egress servers. Hiding a node in the
   client is presentation, not an access-control boundary.
6. SSH is used only for enrolment and repair. The SSH password is accepted from
   a hidden interactive field, retained only in memory, never passed in process
   arguments, never logged and never persisted. Public-key authentication is
   preferred.
7. An unknown SSH host key is displayed to the operator and rejected until its
   SHA-256 fingerprint is explicitly accepted. Host-key verification cannot be
   disabled.

## 4. Persistent model

Authoritative control state is stored under `/etc/neproto/cluster` with
directory mode 0700 and mode-0600 state/key files. Service-readable peer
runtime directories created for an enrolled node use mode 0750; private peer
configuration and credentials remain mode 0600. State updates use a lock,
strict JSON decoding, validation, fsync, atomic rename and a backup of the last
accepted revision.

### Node

```text
id, name, region, role, public_identity, public_addresses,
np2_endpoint, enabled, client_visible, credential_id,
host_key_sha256, provisioned_at, updated_at
```

Runtime-only node health is not part of the signed configuration:

```text
state, last_seen, latency_ms, active_sessions, rx_bytes, tx_bytes,
version, error_category
```

No payload, destination, user secret or SSH credential is included.

### Route

```text
id, name, priority, enabled, source(admin|client), mandatory,
match(domain_suffixes, cidrs, geoip_countries, geosite_categories,
port_ranges, protocols),
action(direct|current|node|chain|block|auto), node_ids
```

Route ordering is deterministic: mandatory admin routes first, then ascending
priority, then route ID. Domain suffix matching is label-boundary aware; CIDRs
are canonical; GeoIP/GeoSite names are validated against the installed V2Fly
datasets; ports are inclusive; protocols are `tcp` or `udp`. Geo matches are
evaluated authoritatively on every NP/2 cluster node. Catalogue-v1 clients
receive a fail-closed non-matching compatibility selector instead of an empty
match that could accidentally become an all-traffic rule.

### User access policy

```text
user_id, allowed_node_ids, allowed_route_ids, allow_auto_selection,
allow_client_routes, revision
```

An empty allowed-node list means only the profile's bootstrap/master node, not
all cluster nodes. Client routes can never override a mandatory admin route.

## 5. Signed client catalogue

The catalogue is transported after normal NP/2 authentication over a bounded
control stream. It is not placed in the public carrier handshake and therefore
does not add a new unauthenticated fingerprint.

```text
version, cluster_id, revision, issued_at, expires_at, user_id,
servers[], admin_routes[], permissions, signature
```

Only nodes and routes allowed for the authenticated user are serialized.
Catalogues are canonical JSON signed over all fields except `signature`, are
limited to 256 KiB and expire after at most 24 hours. A cached unexpired
catalogue remains usable during a temporary control-plane outage. Revocation is
enforced by servers immediately and does not wait for client cache expiry.

## 6. Inter-node NP/2 data plane

The ingress evaluates the effective route before the target is opened. This is
applied to both TCP and bounded UDP associations, including continuity flows.
For a node or chain action it opens a dedicated authenticated NP/2 relay stream
to the next node. Relay metadata is cell-encrypted and contains:

```text
route_id, final_target, remaining_hops, visited_node_ids, trace_id
```

Every node validates the cluster credential, route revision, user permission,
destination policy, hop budget and that its own ID is absent from the visited
set. Unknown nodes, loops, stale policies and excessive hops fail closed. One
authenticated relay session is retained per configured node, the pool is
bounded by the 32-node cluster limit, failed sessions are evicted, and all
sessions close with the server runtime. User traffic is never sent through SSH.

The client cannot request an arbitrary unassigned egress. A client route is a
bounded encrypted hint containing the target and requested action. It is
re-authorised first by the ingress and again by the master against the current
user access policy. A requested route is accepted only when client routes are
enabled and every requested node is assigned to that user. A matching mandatory
administrator route overrides the hint. A client-side `block` action is
enforced locally and is never trusted as a server instruction.

## 7. SSH enrolment lifecycle

1. Validate IP/hostname, port, login, node name and region locally.
2. Connect with a bounded timeout and verify the host-key fingerprint.
3. Check supported Linux distribution, architecture, disk, memory and ports.
4. Stream the pinned server bundle and a one-time bootstrap document to a
   random root-owned staging directory. No user input is interpolated into a
   remote shell command.
5. Run the non-interactive installer, validate configuration and start health
   endpoints.
6. Authenticate a real NP/2 inter-node session, validate the peer principal and
   require a successful protocol attestation before committing the node to the
   cluster catalogue.
7. On failure, remove staging data and leave the previous remote installation
   active. A partially enrolled node is never published to clients.

## 8. `np` server panel

The main workspace adds `CLUSTER` and `ROUTES` tabs.

`CLUSTER` displays cluster revision plus every node's reachability state, name,
region, role and client visibility. It supports SSH add/provision,
enable/drain, publish/hide, synchronize users and remove. A selected non-master
node can be granted to or removed from one user; the credential is synchronized
to the proposed edge set before the catalogue permission is committed, and a
failed pre-sync is rolled back. Periodic authenticated telemetry (version,
sessions and byte counters) is a post-RC hardening item; current `UP` is a
bounded TCP reachability signal, while enrolment uses authenticated NP/2
attestation.

`ROUTES` displays priority, match summary, action, assigned-user count and
status. Its create wizard uses selectors for Domain/IP-CIDR/GeoIP/GeoSite,
enabled egress nodes, and one or more active users; no credential or node ID has
to be retyped. It supports create, enable/disable, delete and per-user
assignment. Assigning a selected-node rule grants only the master plus that
egress; `auto` grants all enabled nodes. Every destructive operation requires
confirmation and produces a bounded audit event.

## 9. iOS client behaviour

1. A bootstrap profile appears immediately and can connect without a catalogue.
2. After authentication the app fetches and verifies the signed catalogue,
   stores it atomically and exposes all authorised servers.
3. Servers display region, reachability, measured latency and administrator
   availability. Selection can be manual or automatic; automatic selection
   never chooses a disabled, expired or unauthorised entry.
4. The Routes screen separates locked administrator routes from local routes.
   A user may add, edit or remove local routes only when permission allows it.
5. PacketTunnel receives an immutable effective snapshot. A catalogue refresh
   does not mutate active flows; new flows use the new revision after an atomic
   switch.
6. Local CIDR/port/protocol routes are enforced for TCP and UDP before a stream
   is opened. The iOS packet tunnel also maintains a bounded, in-memory DNS
   attribution cache for A and AAAA traffic carried inside NP/2. A query is
   registered before it is transmitted; only a matching successful response
   may associate an answer address with the original canonical question name,
   and the association expires no later than the DNS TTL or one hour. New TCP
   and UDP flows use that attributed name in their encrypted NP/2 target, which
   allows authoritative domain and GeoSite rules to match numeric TUN
   destinations. Unsolicited, malformed, expired, oversized and unmatched DNS
   responses fail closed and never populate the cache. When a new TCP/80 or
   TCP/443 flow has no DNS attribution, the TUN dialer may defer only that flow's
   NP/2 OPEN until it receives a bounded first application flight. A complete,
   structurally valid TLS ClientHello SNI or HTTP Host value is then used as the
   encrypted target name. The sniff buffer is capped at 16 KiB, has a finite
   deadline, is never logged or persisted, and falls back to the original
   numeric target for unknown, malformed or incomplete traffic. This fallback
   covers pre-existing OS DNS cache entries and application-owned encrypted DNS
   without weakening TLS or inspecting encrypted application data. Numeric
   flows that yield no validated name remain subject only to CIDR and GeoIP
   rules.
   The `direct` action means egress from the currently selected NP/2 server,
   not an iOS VPN bypass.

## 10. Compatibility and rollout

- Capability name: `cluster_catalog_v1`.
- Capability is negotiated only after NP/2 authentication.
- Cluster and relay paths are disabled by default on upgraded installations.
- Existing QR links, profiles and direct server connections remain valid.
- State migrations are versioned and reversible from the last-good snapshot.

## 11. Verification gates

Production-ready means all gates below pass; a build alone is insufficient.

- Unit and fuzz tests for validation, route ordering/matching, catalogue
  canonicalization/signatures, replay prevention, hop/loop checks and merge.
- Filesystem integration tests for permissions, atomicity, corruption recovery
  and concurrent writers.
- SSH integration tests against disposable containers for unknown host keys,
  wrong credentials, unsupported hosts, interrupted upload, rollback and
  idempotent repair.
- Multi-node localhost/container E2E for direct, selected egress, two-hop chain,
  node loss, credential rotation, revocation and catalogue refresh.
- `go test ./...`, race detector for cluster/control packages, vet, Swift unit
  tests and iOS device build.
- A real two-VPS canary and physical-device connect/switch/route/reconnect test
  before calling the feature live.

## 12. Security exclusions

- No stored SSH passwords, `sshpass`, password in argv/environment, or automatic
  acceptance of host keys.
- No arbitrary remote command editor in the panel.
- No unauthenticated cluster API, private key distribution, destination logs,
  unbounded catalogues, unlimited chains or client-side-only authorization.
- No claim that a multi-hop route makes traffic anonymous or impossible to
  classify.
