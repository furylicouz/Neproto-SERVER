# Implementation Plan: NeProto Self-Hosted Deployment Manager

## Architecture Decisions

- Preserve the NP/2 authentication wire format: the server checks a bounded set of per-user keys without transmitting a user ID.
- Keep the operator API in `neprotoctl`; keep OS bootstrapping in `install.sh`.
- Share one state layout and configuration renderer between Docker and Bare Metal.
- Treat QR as a secret export format and keep import parsing in a shared, independently tested contract.

## Phase 1: Contracts and Authentication Foundation

- [ ] Add canonical onboarding payload/URI package and tests.
- [ ] Add active credential directory loading with strict permissions and duplicate rejection.
- [ ] Add bounded multi-key authentication while retaining legacy single-secret clients.
- [ ] Extend server configuration and deployment examples.

Checkpoint: `go test ./internal/onboarding ./internal/credentials ./internal/protocol ./internal/session ./internal/config ./internal/app`.

## Phase 2: Management Surface

- [x] Add deterministic state renderer and safe atomic file helpers.
- [x] Implement `neprotoctl` status/doctor/service/logs commands and service snapshot API.
- [x] Implement user add/list/export/rotate/revoke with QR output.
- [x] Implement domain set and backup/restore.
- [x] Add the interactive `np` dashboard over the same non-interactive commands.
- [x] Install the relative `/usr/local/bin/np` alias in both deployment modes.

Checkpoint: isolated root tests prove no operation escapes its configured root and every mutation has rollback data.

## Phase 3: Installation Backends

- [ ] Build Bare Metal backend from existing hardened units.
- [ ] Build Docker image and Compose backend with host networking and dropped privileges.
- [ ] Implement supported-OS dependency installation and preflight checks.
- [ ] Implement first-run wizard and idempotent upgrade path.
- [x] Add release archive builder and checksum manifest.

Checkpoint: clean temporary-root installs for both modes; Docker Compose config validates when Docker is present.

## Phase 4: iOS QR Onboarding

- [ ] Add Swift onboarding URI model and malicious-input tests.
- [ ] Extend server profile with credential metadata without putting secrets in Codable storage.
- [ ] Add AVFoundation QR scanner and camera usage description.
- [ ] Add atomic ProfileStore import into Keychain + profile persistence.
- [ ] Build/install on physical iPhone and scan a generated test credential.

Checkpoint: Swift tests pass, signed physical-device build succeeds, and imported user authenticates.

## Phase 5: Release Verification

- [ ] Run all Go/Swift/shell tests, vet, builds, and E2E probes.
- [ ] Perform secret-leak scan of bundle, logs, process arguments, and generated files.
- [ ] Verify backup/restore and user revocation on an isolated deployment.
- [ ] Produce operator runbook and migration guide from the current single-secret VPS.

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Multi-key verification amplifies unauthenticated work | High | Cap active users at 256, pre-derive auth keys, check every candidate without early return, retain session/backlog limits |
| Docker host networking reduces network isolation | Medium | Required for loopback backend and WebRTC ICE correctness; compensate with read-only containers, dropped capabilities, system destination policy, explicit documentation |
| QR disclosure grants access | High | Explicit export only, warning, `0600` files, Keychain import, no logging |
| Domain change invalidates clients | Medium | Confirmation, backup, verification, mandatory re-export notice |
| Dirty existing worktree | High | Touch only scoped files, preserve prior changes, verify diffs per slice, no reset/revert |
