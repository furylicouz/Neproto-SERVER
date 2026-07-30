# NP/2 Web Administration Contract

## Scope

NeProto Web is the browser counterpart of the interactive `np` console. It
must expose the same installed-server management capabilities without
changing the NP/2 wire protocol or bypassing the existing `internal/admin`,
cluster, provisioning, GeoData, validation, backup, and readiness logic.

The web interface reuses the dashboard template already shipped in
`neproto-web`. Product pages adapt existing Login v2, Default Dashboard,
Users, Infrastructure, Tasks, Roles, Analytics, and system-update components.
Template-gallery routes remain examples and are not used as management state.

## Security and privilege boundary

- The public Next.js process remains unprivileged.
- Privileged operations run in `neprotoctl web-api-server`, a root-owned local
  service listening only on `/run/neproto/control.sock`.
- The socket is `0660` and belongs to the installed NeProto service group. It
  is never exposed on TCP, Caddy, or the public Docker network namespace.
- Every public `/api/admin/*` request requires a valid signed administrator
  session. State-changing requests additionally require same-origin
  validation and a bounded JSON body.
- The Next.js proxy forwards only a fixed method/path allowlist. It never
  constructs a shell command.
- The control service accepts bounded JSON, rejects unknown fields, validates
  identifiers before file or network operations, serializes mutations, caps
  output, and returns stable public error categories.
- SSH passwords are accepted only for node enrolment, held in memory, sent
  over the local Unix socket, zeroed after use, and never placed in URLs,
  process arguments, files, jobs, or logs.
- Client credentials are returned only by explicit create/export operations
  and use `Cache-Control: no-store`.

## Management surfaces

| Web page | Reused template | Console parity |
|---|---|---|
| `/dashboard` | Default dashboard cards/charts/table | status summary, host telemetry, services, users, cluster nodes, routes, backups, recent events |
| `/dashboard/users` | Users table | add, export URI/JSON/manual/QR, rotate, revoke, delete, cluster access |
| `/dashboard/cluster` | Infrastructure | node health, enrolment with verified SSH host key, enable/drain, publish/hide, user assignment, credential sync, removal |
| `/dashboard/routes` | Tasks table and dialogs | domain/IP/GeoIP/GeoSite rules, current/direct/block/auto/node/chain actions, user assignment, enable/disable, delete |
| `/dashboard/services` | Analytics/status cards | start, stop, restart, configuration validation, last 200 sanitized events |
| `/dashboard/settings` | Roles/forms/cards | domain change, production/compatibility policy, installed addresses and web endpoint |
| `/dashboard/backups` | Invoice/list cards and dialogs | create, list, verified restore with recovery rollback |
| `/dashboard/system/updates` | Existing update panel | unified signed server and web update |

The storage view from `np` is represented by real read-only dashboard and
settings metadata; secret file contents and private keys are never exposed.

## Local control API

The local API is versioned under `/v1`. Successful responses are JSON. Errors
use `{ "error": "stable_category", "message": "bounded operator message" }`.

### Read operations

- `GET /v1/overview`
- `GET /v1/users`
- `GET /v1/users/{id}/export?format=uri|json|manual`
- `GET /v1/cluster`
- `GET /v1/routes`
- `GET /v1/geodata`
- `GET /v1/services`
- `GET /v1/logs`
- `GET /v1/settings`
- `GET /v1/backups`
- `GET /v1/jobs/{id}`

### State-changing operations

- `POST /v1/doctor`
- `POST /v1/users`
- `POST /v1/users/{id}/rotate`
- `POST /v1/users/{id}/revoke`
- `POST /v1/users/{id}/cluster-access`
- `DELETE /v1/users/{id}`
- `POST /v1/cluster/host-key`
- `POST /v1/cluster/enrol`
- `POST /v1/cluster/sync-users`
- `POST /v1/cluster/nodes/{id}/enable`
- `POST /v1/cluster/nodes/{id}/publish`
- `POST /v1/cluster/nodes/{id}/assign-user`
- `DELETE /v1/cluster/nodes/{id}`
- `POST /v1/routes`
- `POST /v1/routes/{id}/enable`
- `POST /v1/routes/{id}/assign-user`
- `DELETE /v1/routes/{id}`
- `POST /v1/geodata/update`
- `POST /v1/geodata/schedule`
- `POST /v1/services/{start|stop|restart|validate}`
- `POST /v1/settings/domain`
- `POST /v1/settings/policy`
- `POST /v1/backups`
- `POST /v1/backups/restore`

Destructive requests carry an operation-specific confirmation token. Long
operations return `202 Accepted` with a job identifier. The UI polls the job,
shows the existing template dialog/progress components, and refreshes affected
queries after completion.

## Dashboard data contract

The dashboard never renders template fixture data. It shows:

- active and revoked user counts;
- healthy and total cluster-node counts;
- enabled and total routing-rule counts;
- NP/2, Web, and Caddy service states;
- host uptime, load, memory, disk, cumulative RX/TX, and sampled RX/TX rates;
- installed version, deployment mode, domain, public addresses, cluster
  revision, GeoData state/schedule, backup count, and bounded recent events.

Unknown or temporarily unavailable values are explicit; they are not replaced
with generated sample metrics.

## Availability and lifecycle

The control service is installed for both bare-metal and Docker deployments.
In Docker mode the web container receives only the Unix socket mount; the
control service remains on the host so it can safely coordinate Compose,
certificate, configuration, and recovery operations.

The installer starts the control service before the web service and verifies
both `/v1/overview` over the Unix socket and `/api/health` through the public
web endpoint.

## Verification gates

- Unit tests cover request limits, path validation, secret redaction,
  confirmations, mutation serialization, and stable error mapping.
- Existing `neprotoctl` behavior tests remain green.
- Next.js tests cover proxy allowlisting and response validation.
- Lint, type checking, and production build pass.
- Installer smoke tests cover both bare-metal and Docker renderings, service
  units, socket permissions, and web payload.
- Browser tests verify Login v2, authenticated dashboard data, page navigation,
  a safe user lifecycle, and confirmation/progress behavior.
