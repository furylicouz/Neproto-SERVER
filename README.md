# NeProto Server

NeProto Server is the unified open-source distribution of the NP/2 server,
Constellation control plane, terminal administration console, and NeProto Web.
One release archive contains everything required for either a bare-metal or
Docker deployment: Go services, Caddy, a pinned Node.js runtime, and the
standalone web application.

Current release: **NP/2 Constellation `np2-0.5.23`**.

NeProto Web is built, installed, supervised, and published by the same server
installer. Its authenticated update screen checks the pinned GitHub repository
and updates the web application plus NP/2 server as one verified release.
Other security-sensitive management operations remain in the local `np`
console.

## Quick setup

Supported hosts: Ubuntu 22.04/24.04/26.04 and Debian 12/13 on AMD64 or ARM64.
Run as `root`; Git is not required:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/furylicouz/Neproto-SERVER/main/install.sh)
```

The standalone bootstrap downloads the immutable release matching the embedded
stable version, verifies its SHA-256 checksum, and opens the full-screen setup
wizard. It selects the writable temporary filesystem with the most free space
and requires at least 2 GiB there. Override it when needed with, for example,
`NEPROTO_TMPDIR=/mnt/large`. Cloning the repository is optional:

```bash
git clone https://github.com/furylicouz/Neproto-SERVER.git
cd Neproto-SERVER
sudo ./install.sh
```

The wizard asks for:

1. Docker or bare-metal deployment;
2. the primary NP/2 domain, for example `vpn.example.com`;
3. an optional web subdomain, for example `admin.example.com`;
4. an optional ACME contact email;
5. final confirmation before changing the host.

For an unattended install:

```bash
sudo ./install.sh \
  --mode bare-metal \
  --domain vpn.example.com \
  --web-domain admin.example.com \
  --email admin@example.com \
  --non-interactive
```

Docker uses the same configuration contract:

```bash
sudo ./install.sh \
  --mode docker \
  --domain vpn.example.com \
  --web-domain admin.example.com \
  --non-interactive
```

## Domains and web publication

The two hostnames have separate responsibilities:

| Setting | Purpose | Example |
|---|---|---|
| Primary domain | NP/2 client connections and carrier ingress | `vpn.example.com` |
| Web subdomain | NeProto Web through HTTPS/Caddy | `admin.example.com` |

Both DNS records must point to the server before installation. When a web
subdomain is supplied, Certbot issues one certificate containing both names,
Caddy terminates HTTPS for the web application, and Node listens only on
`127.0.0.1`.

If the web subdomain is left blank, the installer publishes NeProto Web on
`http://SERVER_IP:3000`. A different fallback port can be selected with
`--web-port`. This mode is intentionally available for first setup and private
networks; for Internet-facing use, prefer a web subdomain with HTTPS or restrict
the port at the host/provider firewall.

Reserved service ports cannot be selected as the web port.

## Network requirements

| Port | Protocol | Purpose |
|---|---|---|
| 80 | TCP | ACME HTTP-01 validation and HTTPS redirect |
| 443 | TCP | NP/2 HTTPS carrier and web HTTPS ingress |
| 443 | UDP | NP/2 HTTP/3/WebTransport |
| 3000 | TCP | Web fallback only when no web subdomain is configured |
| 40000-40100 | UDP | WebRTC carrier range |

The installer does not rewrite SSH or provider firewall rules. Open only the
ports required by the selected publication mode.

## What the installer deploys

| Component | Bare metal | Docker |
|---|---|---|
| NP/2 data plane | `neproto-server.service` | `neproto/server:local` |
| Constellation manager | `/usr/local/bin/neprotoctl` and `np` | Host command controlling the deployment |
| TLS and ingress | `caddy.service` | `neproto/caddy:local` |
| NeProto Web | `neproto-web.service` | `neproto/web:local` |
| Geodata updates | systemd timer | Host-managed cluster update job |
| Unified release updater | systemd path and timer | Host-managed verified update job |

All runtime artifacts are versioned together. The target server does not need
Go, npm, or a JavaScript dependency tree.

## Server management

After installation, open the terminal control panel:

```bash
np
```

The panel starts automatically for an interactive root SSH login and includes
service health, users and client exports, cluster nodes, GeoIP/GeoSite routes,
domain settings, logs, backups, and recovery actions. Stable automation remains
available through `neprotoctl`:

```bash
neprotoctl doctor
neprotoctl status
neprotoctl user list
neprotoctl service restart
```

Web service checks:

```bash
systemctl status neproto-web                 # bare metal
docker compose -f /opt/neproto/compose.yml ps web  # Docker
curl -fsS https://admin.example.com/api/health
```

The installer creates a separate high-entropy web administrator secret. Read
it only from an administrator terminal, then use it on the NeProto Web login
screen:

```bash
sudo cat /etc/neproto/web-admin.secret
```

## Repository layout

```text
cmd/                 NP/2 server, client, laboratory, and control binaries
internal/            Protocol, carriers, sessions, proxy, cluster, and admin
clients/ios/          Native iOS client sources
clients/windows/      Native Windows VPN client, service, and installer
mobile/               Mobile bridge/runtime
neproto-web/          Next.js NeProto Admin frontend
deploy/package/       Production installer, services, containers, and tests
docs/                 Protocol contracts, decisions, plans, and reports
.github/workflows/    CI and immutable release packaging
```

## Development and verification

Required toolchains are pinned in the repository (`go.mod`, the web lockfile,
and the release builder).

```bash
go test -race -coverprofile=coverage.out ./...
go vet ./...
go build ./cmd/...

cd neproto-web
npm ci --ignore-scripts
npm test
npm run lint -- --max-diagnostics=100
npm run build
```

Build the complete Linux AMD64/ARM64 release:

```bash
deploy/build-server-bundle.sh "$(cat VERSION)" dist
```

Build the Windows 10/11 x64 client and its single-file installer from
PowerShell on Windows:

```powershell
.\clients\windows\build.ps1
```

The resulting `dist/windows/NeProto-Setup-<version>-x64.exe` installs the WPF
application, the LocalSystem NP/2 tunnel service, and the signed Wintun driver
payload. The desktop application runs without elevation; only setup and
removal request administrator approval. Production releases must Authenticode
sign the setup bootstrap, application, service, uninstaller, and Wintun DLL.

The release builder compiles the Go programs and pinned Caddy version, verifies
the pinned Node.js distributions, builds Next.js standalone output, normalizes
archive permissions, and emits both the archive and its checksum.

## Releases and updates

Pushing a tag equal to the contents of `VERSION` (for example `np2-0.5.23`)
triggers the release workflow. It builds the complete server bundle and the
Windows x64 setup, runs isolated bare-metal/Docker lifecycle tests plus Windows
payload verification, and publishes both artifacts with their checksums to
GitHub Releases.

The web `Updates` screen shows the installed version, the latest stable GitHub
release, the check time, and the backend progress state. It checks automatically
when opened or focused if the persisted result is older than 15 minutes.
`Update NeProto`
starts one authenticated API operation. The root updater constructs fixed asset
URLs for `furylicouz/Neproto-SERVER`, verifies the release SHA-256, rejects
unsafe archive entries, reconstructs the existing Docker/bare-metal topology,
creates a backup under `/var/backups/neproto`, and updates server plus web. The
administrator secret is included in the transactional backup, restored on any
unexpected change, and verified by a local authenticated request before the
installer reports success.

The same screen manages verified GeoIP/GeoSite data and its cluster-wide update
schedule. Release availability is also refreshed by the backend at boot, every
six hours, and by the web `Check for updates` button. Operational status is
available with:

```bash
systemctl status neproto-update.path neproto-update-check.timer
journalctl -u neproto-update.service -n 100 --no-pager
```

Rerunning `sudo ./install.sh` remains the supported recovery path.

## Security

- TLS/DTLS verification is never disabled.
- Client exports and QR codes are bearer credentials; treat them as secrets.
- The NP/2 backend binds to loopback behind Caddy by default.
- Web binds to loopback when an HTTPS web subdomain is configured.
- Dashboard and update APIs require an HMAC-signed HttpOnly administrator
  session; the browser never receives root privileges or arbitrary commands.
- Generated credentials, keys, local builds, and release artifacts are ignored
  by Git.
- Report vulnerabilities privately before publishing exploitation details.

## License and attribution

NeProto source code is distributed under the [MIT License](LICENSE).
`neproto-web/LICENSE` retains the MIT attribution for the original dashboard
template. Additional third-party notices are documented in
[`docs/THIRD_PARTY.md`](docs/THIRD_PARTY.md).
