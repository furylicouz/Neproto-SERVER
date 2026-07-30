# NP/2 Constellation Cluster and Routing Implementation Plan

Status: release-candidate implementation complete; production canary pending

Date: 2026-07-19

The normative contract is `NP2-CLUSTER-ROUTING-SPEC.md`. Every behavioural
task follows RED -> GREEN -> REFACTOR and preserves single-server compatibility.

| Phase | Implementation | Remaining release gate |
|---|---|---|
| 1. State and contracts | Complete | None in local gate |
| 2. Signed catalogue | Complete | Real-device refresh/revocation exercise |
| 3. NP/2 relay and routing | Complete for TCP/UDP and two-hop chains | Two independent VPS canary |
| 4. SSH provisioner | Complete with pinned host keys and protocol attestation | Enrol a clean external VPS |
| 5. `np` UX | Complete | Operator acceptance on production terminal sizes |
| 6. iOS | Complete and unsigned-build verified | Signed physical-device flow test |
| 7. Packaging/rollout | Local/container gates complete | Live canary, rollback drill and device evidence |

## Phase 1 - contracts and persistent control state

1. Add bounded node, route, user-access and cluster-state models.
2. Add deterministic route validation, matching and effective-policy merge.
3. Add Ed25519 canonical catalogue signing and verification with monotonic
   revision checks.
4. Add atomic cluster store under the manager root and last-good recovery.
5. Expose cluster operations through `internal/admin.Manager`.

Exit gate: unit/fuzz/filesystem tests pass; secrets never appear in state JSON.

## Phase 2 - authenticated catalogue control plane

1. Negotiate `cluster_catalog_v1` after normal NP/2 authentication.
2. Add bounded request/response control records and per-user catalogue filter.
3. Add catalogue cache, expiry and rollback protection in the mobile binding.
4. Add counters for accepted/rejected/stale catalogue operations without user
   or destination labels.

Exit gate: legacy peers still connect; tampered, oversized, expired and rolled
back catalogues fail closed.

## Phase 3 - inter-node relay and route enforcement

1. Add relay metadata codec with hop budget and visited-node set.
2. Add unique node credentials and server-side access checks.
3. Add bounded authenticated session pools for next-hop nodes.
4. Route TCP and UDP through direct, node and chain actions.
5. Add drain, retry and deterministic failure categories.

Exit gate: real-carrier multi-node E2E passes direct, one-hop, two-hop, node
loss, loop rejection and credential rotation.

## Phase 4 - secure SSH provisioner

1. Add validated connection inputs, hidden password source and host-key pinning.
2. Add remote preflight and pinned bundle streaming to a fixed staging layout.
3. Add non-interactive installation, health attestation, commit and rollback.
4. Add idempotent repair and uninstall-from-cluster operations.

Exit gate: disposable-host matrix passes interruption and rollback tests; no
password is observable in process listings, logs or files.

## Phase 5 - `np` cluster and routes UX

1. Add CLUSTER and ROUTES workspaces, function-key navigation and compact mode.
2. Add node status/traffic/version panels and provisioning progress.
3. Add node lifecycle dialogs including host-key confirmation.
4. Add route editor, user assignment and destructive confirmations.

Exit gate: tcell simulation tests cover every command and error path; selecting
an action never exits the panel unexpectedly.

## Phase 6 - iOS catalogue, servers and routes

1. Add signed catalogue, server availability and route models to NeProtoCore.
2. Add atomic cache, sync coordinator and merge tests.
3. Add multiple authorised servers and auto/manual selection to the Servers UI.
4. Add Routes UI for locked admin rules and editable local rules.
5. Pass an immutable effective routing snapshot into PacketTunnel and enforce
   CIDR/port/protocol rules for new TCP and UDP flows.

Exit gate: Swift tests, unsigned Mac build, signed device build and device flow
tests pass; stale/tampered catalogues are visible as errors and never applied.

## Phase 7 - packaging, observability and production rollout

1. Include cluster state directories, backup/restore and migration in bare-metal
   and Docker installers.
2. Add health aggregation, bounded audit events and cluster diagnostics.
3. Run full Go/Swift/race/vet/fuzz suites and disposable multi-node E2E.
4. Run a two-VPS canary, physical-device server switching and route validation.
5. Document upgrade, rollback, disaster recovery and operator workflows.

Exit gate: all evidence is captured in a release report. Any unavailable real
device or VPS gate remains explicitly pending and prevents a live claim.
