# NeProto NP/2 unified server bundle

Run as root on Ubuntu 22.04/24.04/26.04 or Debian 12/13:

```bash
tar -xzf neproto-server-bundle-np2-0.5.26.tar.gz
cd neproto-server-bundle-np2-0.5.26
./install.sh
```

The bundle includes NP/2, the Constellation control plane, Caddy, a pinned
Node.js runtime, and the production NeProto Web standalone application. The
host never downloads npm dependencies during installation.

The installer also creates a separate web administrator secret and enables the
unified update API. The `Updates` screen checks the pinned GitHub repository;
one button downloads the release archive and checksum, verifies and safely
extracts them, backs up the installed topology, and updates NeProto Web plus
the NP/2 server together. The administrator secret is transactionally
preserved and exercised by a local login check before success. The browser
only creates a fixed authenticated job;
the root update worker accepts no command, URL, version, or path from the UI.
The screen also owns cluster-wide GeoIP/GeoSite updates and their automatic
schedule; software checks run automatically when the visible status is stale.

With no arguments, `install.sh` opens the same full-screen Constellation UI as
the server panel. The transaction stays inside that interface from Docker/Bare
Metal selection through domain/email entry, live progress, service validation,
and the final result. It installs Certbot, obtains one
certificate shared by Caddy on TCP 443 and NeProto WebTransport on UDP 443,
creates three private routes, publishes NeProto Web, configures renewal hooks,
and validates the result after starting services. The primary domain is used
for NP/2. A separate optional web domain is published through HTTPS; when it is
left empty, the web application listens publicly on TCP 3000 (or `--web-port`).
It installs a guarded `/etc/profile.d` hook but
does not change `sshd` or firewall rules. For a line-oriented recovery install,
run `NEPROTO_CLASSIC_INSTALL=1 ./install.sh`.

Before installation, DNS must already point to the server and inbound TCP
80/443, UDP 443, and the configured WebRTC UDP range (default 40000-40100)
must be allowed. On the next interactive root SSH login, the panel starts
automatically. `q` closes it and returns to the normal shell. You can also open
it manually:

```bash
np
```

The panel manages status/diagnostics, users and complete manual/URI/QR client
settings, permanent deletion of revoked users, cluster nodes, and selectable
Domain/IP/GeoIP/GeoSite traffic routes, plus services, logs, domain changes,
feature policy, and backups. Workspaces, forms,
confirmations, results, and QR output remain inside the full-screen UI. Stable
automation commands remain available through `neprotoctl`. The native offline
Braille map is inspired by MapSCII; arrows pan, `a`/`z` zoom, and `c` resets it.
Use `F1`-`F8` to switch workspaces and `F10` to quit. For a line-oriented
recovery panel, run `NEPROTO_CLASSIC_UI=1 np`.

To disable autostart persistently, create
`/etc/neproto/console.no-autostart`; remove that file to enable it again. Set
`NEPROTO_NO_AUTO_UI=1` for a one-session bypass. Non-interactive SSH commands,
SCP/SFTP, cron, and forced commands never launch the console.

`neprotoctl doctor` verifies DNS, HTTPS and a real HTTP/3 WebTransport session.
Domain changes issue the replacement certificate before restarting services
and roll back state plus certificate selection if any readiness check fails.

QR/URI exports are bearer credentials. Display them only to the intended
client and revoke a user immediately if an export is disclosed.

The installer downloads checksum-verified GeoIP and GeoSite databases into
`/etc/neproto/geodata`. Refresh every cluster node from `np` -> `ROUTES` -> `G`
or with `neprotoctl geodata update --cluster=true`. Press `T` to select the
automatic schedule; verified weekly updates are enabled by default. The legacy
`/usr/local/lib/neproto/update-geodata` recovery helper does not coordinate or
hot-reload a cluster.
