# NP/2 Unified Update Contract

Status: production contract for `np2-0.4.x` unified server and web releases.

## Scope

One checksum-verified release bundle updates the NP/2 server, `neprotoctl`, the privileged
updater, Caddy, the embedded Node runtime, NeProto Web, unit files, Docker
definitions, and deployment documentation. The browser never runs shell
commands and never chooses a repository, URL, file path, version, or installer
argument.

The only trusted release source is:

```text
https://github.com/furylicouz/Neproto-SERVER
```

Release tags MUST match `np2-MAJOR.MINOR.PATCH`. Draft and prerelease entries
MUST be ignored. An available version MUST be strictly newer than the running
version.

## Components and privilege boundary

| Component | Identity | Responsibility |
|---|---|---|
| NeProto Web | `neproto` | Read public update status and create a fixed update request after admin authentication |
| `neproto-update.path` | systemd | Watch the fixed request path |
| `neproto-update.service` | root, one-shot | Start the updater with fixed arguments |
| `neproto-update-check.timer` | systemd | Refresh release availability every six hours |
| `neproto-updater` | root | Check, download, verify, safely extract, back up, install, and report progress |

The writable browser boundary is exactly the two fixed marker paths:

```text
/var/lib/neproto/update/inbox/apply
/var/lib/neproto/update/inbox/check
```

The request contains no user-controlled command or URL. The status file is
root-owned and group-readable:

```text
/var/lib/neproto/update/status.json
```

## Authentication

The installer creates a random web administrator secret once and preserves it
across upgrades. It is stored at `/etc/neproto/web-admin.secret`, mode `0640`,
owned by `root:neproto`, and is never written to logs, URLs, Git, or the update
status.

Successful login creates an authenticated, `HttpOnly`, `SameSite=Strict`
session cookie signed with HMAC-SHA-256. Update POST requests also require a
same-origin browser request. Authentication failures return a stable generic
error and never reveal whether a secret file exists.

## Web API

### `GET /api/system/update`

Returns the bounded status document. It never starts an update.

```json
{
  "schema": 1,
  "state": "idle",
  "current_version": "np2-0.4.0",
  "available_version": "np2-0.4.1",
  "update_available": true,
  "progress": 0,
  "message": "Update available",
  "updated_at": "2026-07-30T00:00:00Z"
}
```

### `POST /api/system/update/check`

Creates the fixed check request and returns `202 Accepted`, including when the
same request is already pending.

### `POST /api/system/update/start`

Creates the fixed request atomically and returns `202 Accepted`. A repeated
request is idempotent and returns `202` with `pending: true`; starting a new
update while an update is already running returns `409 Conflict`. Both POST
request bodies must be empty; arbitrary payloads are rejected.

### `GET /api/system/update/progress`

Returns the same bounded status contract for the modal polling loop. It never
starts an update.

## State machine

```text
idle -> checking -> downloading -> verifying -> extracting
     -> backing_up -> installing -> restarting -> succeeded
                                             \-> failed
```

`progress` is monotonic within one attempt and is always in `0..100`.
`message` and `error_code` are UTF-8, single-line, and bounded. Status writes
are atomic. The installer creates a recovery backup before replacing runtime
files, and a failed attempt exposes a sanitized failure category.

## Verification and extraction

The updater MUST:

1. Use HTTPS with bounded response sizes and deadlines.
2. Construct asset URLs from the validated tag and fixed repository.
3. Download both the archive and its `.sha256` asset.
4. Require exactly one SHA-256 entry for the expected archive filename.
5. Verify the digest before extraction.
6. Reject absolute paths, `..`, links, devices, sockets, excessive entries,
   excessive expanded bytes, and files outside the single expected root.
7. Execute only the extracted regular `install.sh` with arguments reconstructed
   from `/etc/neproto/installation.json` and the existing ACME email.
8. Apply a total update deadline and retain bounded progress output.

## UI contract

The sidebar contains an `Updates` / `Обновления` link outside the template
catalog. The page reuses the existing dashboard Card, Badge, Button, Progress,
AlertDialog, Skeleton, and Alert components. While an update is active, the
existing AlertDialog overlay blurs the page and a modal displays the current
stage and progress. The modal cannot be dismissed while installation is active.

The page loads the persisted update status immediately and requests a fresh
GitHub check when that status is older than 15 minutes. The same bounded check
runs when the visible tab regains focus and while the page remains open; active
checks and installations suppress duplicate requests. The manual check remains
available for an explicit administrator refresh.

The same workspace owns GeoIP/GeoSite lifecycle controls: current verified
state, automatic schedule, and a cluster-wide update job with bounded progress.
Route creation consumes these databases but does not manage their lifecycle.

No new visual language, colors, spacing scale, or custom modal primitive is
introduced.
