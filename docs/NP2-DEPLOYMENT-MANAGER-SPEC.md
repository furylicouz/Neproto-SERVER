# Spec: NeProto Self-Hosted Deployment Manager

Status: Approved for implementation

Date: 2026-07-17

## Objective

Package the NP/2 server, Constellation control plane, and NeProto Web as one
repeatable self-hosted product. A server operator runs one interactive
installer, chooses Docker Compose or Bare Metal, configures the NP/2 domain
and optional web domain, and then manages the node through one stable
`neprotoctl` interface.

The product must support independently revocable users. Every user receives a unique 256-bit credential and a versioned QR onboarding profile. The iOS app scans the QR, validates every field, stores the credential in Keychain, and stores only non-secret profile data in application preferences.

## Supported Systems

- Ubuntu 22.04/24.04/26.04 LTS and Debian 12/13.
- `amd64` and `arm64`.
- Root execution for installation and node administration.
- Docker mode uses Docker Engine with the Compose v2 plugin.
- Bare Metal mode uses hardened systemd units and Caddy 2.11.4.

Other distributions fail before changing system state.

## Operator Interface

The release archive exposes:

```text
./install.sh
neprotoctl menu
neprotoctl status
neprotoctl doctor
neprotoctl user add --name NAME [--profile quiet|web|interactive]
neprotoctl user list
neprotoctl user revoke --id ID
neprotoctl user rotate --id ID
neprotoctl user delete --id ID --confirm DELETE
neprotoctl user export --id ID --format manual|uri|json|qr|png
neprotoctl domain set --domain DOMAIN
neprotoctl service start|stop|restart
neprotoctl logs [--follow]
neprotoctl backup create
neprotoctl backup restore --path PATH
np
```

Without arguments, `neprotoctl` opens the interactive menu. Non-interactive commands have stable exit codes and are suitable for automation.
`np` is the installed short alias for the same binary and always opens that
menu when invoked without arguments.

### Interactive `np` dashboard

Every screen starts with the NeProto ASCII wordmark and a compact, secret-free
node summary: NP/2 version, deployment backend, domain, configured public
addresses, NP/2/Caddy state, and active/revoked user counts. The menu provides:

1. node status and production diagnostics;
2. user list, creation, manual/QR/URI/JSON/PNG export, credential rotation,
   revocation, and confirmed permanent deletion of revoked users;
3. service start, stop, restart, and recent/following logs;
4. validated domain replacement with backup, configuration validation,
   service restart, HTTPS readiness check, and rollback on failure;
5. backup creation, listing, and confirmed restore;
6. version/help information and clean exit.

User-facing selections use numbered users rather than requiring operators to
retype credential IDs. Destructive actions require an explicit typed
confirmation. Secret exports are never included in the dashboard summary,
history, diagnostics, or logs.

## Installation Flow

1. Verify root, supported OS/architecture, free ports, DNS prerequisites, and bundle integrity.
2. Ask for `docker` or `bare-metal` deployment.
3. Install only the dependencies required by the chosen backend.
4. Ask for the lowercase NP/2 domain and confirm its resolved public addresses.
5. Ask for an optional, distinct lowercase web domain. If omitted, publish the
   web runtime on a bounded public port (default TCP 3000).
6. Generate three independent random private ingress paths.
7. Install a decoy website, Caddy, NP/2 server, manager, pinned Node runtime,
   standalone NeProto Web payload, configuration, and service definitions.
8. Create the first user and offer one sensitive bundle containing manual
   settings, import URI and QR only after explicit confirmation.
9. Validate configuration before starting services.
10. Start services, wait for HTTPS and web readiness, run an authenticated NP/2 probe, and display the management menu.

Every mutation creates a root-only backup before replacement. A failed validation restores the previous state.

## Deployment Backends

### Bare Metal

- `neproto-server` runs as the unprivileged `neproto` user.
- Caddy runs as the unprivileged `caddy` user.
- NP/2 HTTP backend remains bound to `127.0.0.1:9080`.
- systemd hardening from the existing units remains mandatory.

### Docker Compose

- Separate, read-only NP/2 and Caddy containers.
- Both use Linux host networking so the NP/2 backend remains loopback-only and WebRTC host ICE/UDP remains valid without unsafe NAT guessing.
- The NP/2 container drops all capabilities and runs as a fixed unprivileged UID/GID.
- Caddy receives only `NET_BIND_SERVICE`.
- Persistent state is bind-mounted from `/etc/neproto`, `/etc/caddy`, `/var/lib/caddy`, and `/var/www/neproto`.
- Images are built from the reviewed release bundle; no source checkout or floating `latest` tag is used at runtime.
- The web container uses the same host network contract, runs without Linux
  capabilities, and contains only the standalone traced Next.js runtime.

## Credential Model

The current NP/2 authentication response intentionally contains no public user identifier. Multi-user authentication preserves that wire privacy and compatibility:

- each active user owns one canonical 32-byte base64url secret file;
- server configuration points at an active credential directory;
- authentication derives an auth key for every active credential and checks all candidate response tags without early return;
- the matched credential ID is retained only as local session metadata;
- duplicate secrets and more than 256 active credentials are rejected at startup;
- revoked credentials are moved out of the active directory and take effect after validated service restart;
- permanent deletion is permitted only after revocation and atomically removes
  the user index entry, archived secrets, and cluster access policy;
- the legacy single `secret_file` remains supported during migration.

Credential enumeration is bounded, deterministic, and never logs secret material.

## User State

```text
/etc/neproto/users/
  index.json                 # non-secret operator metadata
  active/<id>.secret         # mode 0600, owner/group limited to NP/2
  revoked/<id>.secret        # mode 0600, not loaded by server
```

User IDs are 128-bit random base64url values. Names are display-only UTF-8 strings of 1-64 characters. Index updates and secret rotation are atomic.

## QR Import Contract

URI prefix:

```text
np2://import/v1/<base64url-without-padding-of-canonical-json>
```

Canonical JSON fields:

```json
{
  "version": 1,
  "credential_id": "128-bit base64url identifier",
  "name": "Device label",
  "server_identity": "vpn.example.com",
  "server_addresses": ["203.0.113.10"],
  "https_path": "/private-route",
  "webrtc_path": "/private-route-2",
  "profile": "web",
  "secret": "32-byte base64url credential"
}
```

The payload is a bearer credential. QR output is produced only on explicit request, terminal output warns the operator, PNG/JSON files use mode `0600`, and no QR URI is written to logs or shell command arguments.

The iOS importer rejects unknown fields, unsupported versions, duplicate/invalid routes, non-public pinned addresses, malformed credentials, oversized payloads, and duplicate credential IDs. The secret is written to Keychain before the profile is persisted. Partial imports are rolled back.

## Domain Changes

`neprotoctl domain set`:

1. validates the new domain and DNS;
2. creates a backup;
3. updates server identity, Caddy site address, and future user exports;
4. validates both configurations;
5. reloads/restarts the selected backend;
6. verifies HTTPS readiness;
7. reports that all client profiles must be re-exported because the authenticated identity changed.

User credentials and private paths are preserved unless the operator separately requests rotation.

## Security Boundaries

Always:

- strict input validation and quoted filesystem operations;
- secrets only in root-owned or service-readable `0600` files and iOS Keychain;
- backups before changes and validation before restart;
- loopback-only backend, explicit UDP range, destination policy enforcement;
- sensitive QR warning and opt-in display.

Ask first:

- firewall changes that could affect SSH/monitoring;
- destructive uninstall or backup restore;
- domain change and credential rotation.

Never:

- store credentials in Git, Compose environment variables, process arguments, logs, URLs sent to web servers, or world-readable files;
- expose SOCKS or the NP/2 backend publicly;
- install from floating image tags;
- silently disable masking, AEAD, validation, or destination policy.

## Commands

```text
Go tests:      C:\Neproto\.tools\go\bin\go.exe test ./...
Go vet:        C:\Neproto\.tools\go\bin\go.exe vet ./...
Go build:      C:\Neproto\.tools\go\bin\go.exe build ./cmd/...
Shell syntax:  bash -n deploy/package/install.sh deploy/package/neprotoctl
Swift tests:   cd clients/ios/Core && swift test
Docker config: docker compose -f deploy/package/docker/compose.yml config
```

## Project Structure

```text
internal/credentials/          credential loading and validation
internal/onboarding/           versioned QR/import contract
deploy/package/install.sh      interactive installer
deploy/package/neprotoctl      stable manager entry point
deploy/package/lib/            shared shell functions
deploy/package/docker/         Dockerfile and Compose definition
deploy/package/templates/      backend configuration templates
deploy/package/tests/          isolated installer/manager smoke tests
clients/ios/Core/              import parser and validation
clients/ios/App/               QR scanner and import UI
```

## Testing Strategy

- Go unit tests for credential loading, duplicate rejection, constant-work matching, import URI parsing, and redaction.
- Authentication integration tests with two users, revocation, legacy compatibility, and invalid credentials.
- Shell tests run against a temporary root with mocked package/service commands; host `/etc`, systemd, Docker, firewall, and SSH are never touched.
- Compose configuration validation when Docker is available.
- Swift unit tests for valid and malicious QR payloads.
- Physical-device build verifies camera permission, QR scanning, Keychain storage, and connection.

## Success Criteria

- A clean supported VPS can be installed through either backend from one release archive.
- Re-running installation is idempotent and preserves credentials/private paths.
- An operator can create, export, rotate, and revoke independent users.
- Revoking one user does not affect other users.
- QR import on iOS creates a working profile without manual field entry.
- No credential appears in logs, process arguments, Compose environment, or world-readable files.
- Failed changes leave the previous working deployment recoverable.
