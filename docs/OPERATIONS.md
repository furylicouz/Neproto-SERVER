# NP/2 Operations Runbook

This runbook installs the server without changing SSH, Zabbix, or firewall state implicitly. Replace every example domain and route. Never store a production secret in the repository, shell history, a process argument, or a generated URL.

## Required inputs

- A primary NP/2 domain whose `A`/`AAAA` records point to the server.
- Optionally, a distinct web subdomain pointing to the same server. Without
  one, NeProto Web is published on TCP 3000 by default.
- Three distinct, randomly generated route components of at least 32 characters.
- Explicit approval for any firewall changes.
- TCP 80/443, UDP 443, and the configured WebRTC UDP range reachable from
  clients. Preserve TCP 22 and existing monitoring ports.

Generate route components locally without printing secret key material:

```sh
openssl rand -hex 24
openssl rand -hex 24
openssl rand -hex 24
```

## Recommended production installer

The versioned release bundle is the supported installation path. The repository
bootstrap downloads the matching release and verifies its SHA-256 checksum:

```sh
git clone https://github.com/furylicouz/Neproto-SERVER.git
cd Neproto-SERVER
sudo ./install.sh
```

The verified bundle performs an idempotent bare-metal or Docker install,
provisions a shared ACME certificate,
creates all three private routes, configures renewal hooks, initializes an
administrator credential, and validates services before start:

```sh
tar -xzf neproto-server-bundle-np2-0.4.1.tar.gz
cd neproto-server-bundle-np2-0.4.1
sudo ./install.sh
sudo np
sudo neprotoctl doctor
```

With no arguments, `install.sh` opens the cinematic Constellation deployment
wizard. Docker/Bare Metal selection, NP/2 domain, optional web domain, ACME identity, live command
output, progress, service start, and the final `doctor` gate all stay inside
the TUI. Use `NEPROTO_CLASSIC_INSTALL=1 sudo ./install.sh` only for recovery on
a terminal that cannot run the native UI.

When a web domain is configured, Caddy publishes NeProto Web at
`https://WEB_DOMAIN` and the Node runtime binds only to loopback. With no web
domain, it binds to `0.0.0.0:3000`; use `--web-port` to select another
non-reserved port and restrict it at the firewall when public HTTP is not
desired.

## Web administrator and unified updates

The installer creates `/etc/neproto/web-admin.secret` once, preserves it on
upgrade, and grants the web process read-only access through the `neproto`
group. Retrieve it only from an administrator terminal and enter it on the web
login screen:

```sh
sudo cat /etc/neproto/web-admin.secret
```

The browser calls the authenticated `/api/system/update` API. It cannot select
a repository or run a shell command. `neproto-update.path` and
`neproto-update-check.path` translate fixed marker files into root-owned
one-shot services. `neproto-update-check.timer` refreshes GitHub availability
every six hours.

```sh
systemctl status neproto-update.path neproto-update-check.path neproto-update-check.timer
journalctl -u neproto-update.service -n 100 --no-pager
cat /var/lib/neproto/update/status.json
```

An update validates the stable `np2-MAJOR.MINOR.PATCH` tag, constructs assets
for `furylicouz/Neproto-SERVER`, verifies SHA-256 before safe extraction, and
executes only the bundled installer with values reconstructed from
`/etc/neproto/installation.json`. The same operation replaces server and web;
the existing installer backup under `/var/backups/neproto` remains the rollback
source.

`np` opens the native full-screen Constellation console. Every workspace,
form, confirmation, QR/result view, service action, domain change, and backup
operation remains inside the screen; selecting an item never suspends the UI
into the calling shell. It shows live host memory and network rates, service
and carrier state, the public node, users, backups, managed storage, and a
native offline Braille world map inspired by MapSCII. Use arrows or `j`/`k`,
`Enter`, `F1` through `F8`, `r` to refresh, `a`/`z` to zoom the map, and `q` to
return to the previous workspace or shell. A terminal of at least 100 columns
by 30 rows is recommended. The console does not display private routes,
certificate contents, traffic destinations, or credentials except when an
operator explicitly requests a client QR.

The release installer places `/etc/profile.d/neproto-console.sh`. It starts
`np` only for an interactive root SSH session with both stdin and stdout bound
to a TTY. SCP/SFTP, forced commands, cron, cloud-init, and non-interactive SSH
are unaffected. `q` returns to the shell. Disable persistent autostart with:

```sh
sudo touch /etc/neproto/console.no-autostart
```

Re-enable it with `sudo rm /etc/neproto/console.no-autostart`. The environment
flag `NEPROTO_NO_AUTO_UI=1` provides a one-session bypass.

For serial consoles, log collection, or recovery from a terminal capability
problem, use the line-oriented compatibility panel:

```sh
sudo NEPROTO_CLASSIC_UI=1 np
```

The installer deliberately does not modify the SSH daemon or firewall rules;
it only adds the guarded interactive-shell hook described above. DNS and
inbound TCP 80/443, UDP 443, and UDP 40000-40100 must be prepared first.
On supported Linux kernels it loads `tcp_bbr` and installs
`/etc/sysctl.d/99-neproto-performance.conf` with BBR plus `fq` queueing for the
outer HTTPS carrier. If BBR is unavailable, the installer removes those two
host-tuning files and retains the operating system TCP defaults instead of
leaving a broken boot-time sysctl setting. Independently of BBR, the installer
uses `/etc/sysctl.d/90-neproto-udp.conf` and
`neproto-udp-buffers.service` to permit the multi-megabyte receive and send
buffers required by QUIC/WebTransport and WebRTC before `neproto-server`
starts. The host-wide default socket sizes remain unchanged. Verify the active
values with:

```sh
sysctl net.ipv4.tcp_congestion_control net.core.default_qdisc \
  net.core.rmem_max net.core.wmem_max
```

The remaining manual sections are for auditing and disaster recovery.

## Build and stage

Build from the reviewed commit with the pinned Go toolchain:

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o neproto-server ./cmd/neproto-server
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o neproto-client ./cmd/neproto-client
```

Create an unprivileged account and install immutable program files:

```sh
sudo useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin neproto
sudo useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
sudo install -o root -g root -m 0755 neproto-server /usr/local/bin/neproto-server
sudo install -d -o root -g neproto -m 0750 /etc/neproto
sudo install -d -o root -g caddy -m 0750 /etc/caddy
sudo install -d -o caddy -g caddy -m 0750 /var/lib/caddy
sudo install -d -o root -g root -m 0755 /usr/share/doc/neproto /var/www/neproto
sudo install -o root -g root -m 0644 docs/OPERATIONS.md /usr/share/doc/neproto/OPERATIONS.md
sudo install -o root -g root -m 0644 deploy/caddy/site/index.html /var/www/neproto/index.html
```

Install the pinned official Caddy release only after verifying the release checksum:

```sh
version=2.11.4
base="https://github.com/caddyserver/caddy/releases/download/v${version}"
curl -fsSLO "$base/caddy_${version}_linux_amd64.tar.gz"
curl -fsSLO "$base/caddy_${version}_checksums.txt"
grep "  caddy_${version}_linux_amd64.tar.gz$" "caddy_${version}_checksums.txt" | sha512sum -c -
tar -xzf "caddy_${version}_linux_amd64.tar.gz" caddy
sudo install -o root -g root -m 0755 caddy /usr/local/bin/caddy
```

## Secret and configuration

Generate the secret into a protected file, then transfer the same file to the client over an authenticated channel. Do not copy its value through a command argument:

```sh
umask 077
/usr/local/bin/neproto-server generate-secret > /tmp/neproto-server.secret
sudo install -o neproto -g neproto -m 0600 /tmp/neproto-server.secret /etc/neproto/server.secret
rm -f /tmp/neproto-server.secret
```

Materialize the server configuration with distinct HTTPS, WebRTC, and HTTP/3
paths. Keep the HTTP backend on loopback, bind WebTransport to UDP 443, and use
the same `/etc/neproto/tls/fullchain.pem` and `privkey.pem` files in Caddy and
NeProto.

Validate before creating any listener:

```sh
sudo -u neproto /usr/local/bin/neproto-server check --config /etc/neproto/server.json
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

## Caddy and systemd

Install the materialized Caddyfile only after validation. Caddy owns TCP 443
with HTTP/1.1 and HTTP/2, proxies the HTTPS/WebSocket and WebRTC signaling
paths, and serves the decoy site. NeProto independently owns UDP 443 with a
WebTransport Extended CONNECT endpoint. Certbot HTTP-01 uses
`/var/www/certbot` and its deploy hook atomically copies renewed files before
restarting both readers.

```sh
sudo install -o root -g root -m 0644 deploy/systemd/neproto-server.service /etc/systemd/system/neproto-server.service
sudo install -o root -g root -m 0644 deploy/systemd/caddy.service /etc/systemd/system/caddy.service
sudo systemctl daemon-reload
sudo systemctl enable --now neproto-server caddy
```

For repeatable installs, `deploy/install-server.sh` performs the account, backup, file installation, route generation, and validation steps. It preserves an existing node's routes and secret on upgrade, refuses an implicit domain migration, and starts services only when `--start` is supplied. It does not modify firewall, SSH, or Zabbix state.

```sh
sudo deploy/install-server.sh \
  --domain vpn.example.com \
  --server-binary ./neproto-server \
  --caddy-binary ./caddy \
  --start
```

Check the result:

```sh
systemctl --no-pager --full status neproto-server caddy
journalctl -u neproto-server -n 100 --no-pager
ss -lntup
curl --fail --silent http://127.0.0.1:9464/metrics | head -n 20
curl --fail --show-error --silent https://vpn.example.com/
```

Expected public listeners are TCP 80/443, UDP 443, and the configured WebRTC
UDP range, plus pre-existing approved administration/monitoring ports. Port
9080 must remain loopback-only. Do not enable a host firewall blindly on a
remote system; inventory and preserve existing access first.

The generated server config includes process-wide and authenticated-user
resource ceilings. Before increasing them, check `LimitNOFILE`, resident
memory, and the staged capacity evidence. Alert at minimum on sustained growth
of `np2_server_rejected_sessions_total`, `np2_server_tcp_limit_rejects_total`,
`np2_server_udp_global_user_limit_rejects_total`,
`np2_server_resource_udp_rate_limit_rejects_total`, authentication failures,
protocol errors, resident memory, and open descriptors. These metrics contain
no credential or destination labels.

## Client

Copy `neproto-client`, the materialized client JSON, and the shared secret to the client. Set the secret to mode `0600`, run `neproto-client check`, then start the client. The SOCKS listener must remain on loopback unless a separately authenticated access layer is designed.

Desktop applications that support SOCKS5 UDP may use `UDP ASSOCIATE` through
the same loopback listener. The TCP control connection must stay open for the
UDP relay lifetime. Fragmented SOCKS UDP packets are intentionally unsupported
and dropped; applications should keep datagrams within their negotiated MTU.

## Pulse rollout, rollback, and Mosaic diagnostics

Pulse is the low-overhead production candidate; Mosaic remains an optional
authenticated v2.2 capability. Upgrade the server binary before writing
`cover_mode=pulse`, then upgrade clients. Cover selection is direction-local
and each mixed-mode combination remains wire-compatible:

1. A Pulse sender emits ordinary authenticated NP/2 cells with bounded padding;
   an `off` or older covered receiver already decodes those cells.
2. An `off` sender remains on the direct AEAD path while the opposite direction
   may independently use Pulse.
3. Mosaic activates only when both peers advertise and select its capability;
   Pulse never advertises it.

Verify one carrier without starting a SOCKS listener:

```sh
neproto-client probe --config /etc/neproto/client.json --carrier https
```

Expected production output:

```text
carrier=https fallback=false authentication=ok
cover=pulse class=pulse transitions=0
```

`cover=off`, `cover=fixed`, or `cover=mosaic` is not an authentication failure.
It identifies the local sender mode. A probe contains no sustained workload,
so zero Mosaic transitions are normal.

Rollback Pulse by setting `cover_mode=off` and restarting the service; no QR,
secret, or cell-format migration is needed. A binary that predates the `pulse`
configuration value will reject it locally even though the cell wire format is
compatible, so change the configuration before rolling that binary back. Do
not infer DPI resistance from a successful probe. Validate throughput,
latency, cover overhead, and packet captures separately on the intended
network.

## Credential rotation

Each client has an independent credential. Rotate or revoke without exposing
another user's secret:

```sh
sudo neprotoctl user list
sudo neprotoctl user rotate --id USER_ID
sudo neprotoctl user export --id USER_ID --format qr
sudo neprotoctl user export --id USER_ID --format manual
sudo neprotoctl user revoke --id USER_ID
sudo neprotoctl user delete --id USER_ID --confirm DELETE
```

Rotation invalidates the previous QR. Permanent deletion is intentionally a
two-step operation: revoke first, then delete the revoked record and archived
credential. `manual` prints the server identity/addresses, credential ID,
secret, traffic profile, private carrier paths, Constellation settings and the
same import URI encoded in the QR. Exports are bearer credentials and must be
shown only to the intended user. Server certificate renewal is independent and
is handled by Certbot plus `neproto-sync-certificate`.

## Constellation cluster operations

Run `np` as root on the authoritative master. Cluster changes are performed
inside the same full-screen panel; an action result is shown in-panel and does
not drop the operator back to the shell.

1. Open `F3 CLUSTER` and press `N` to enrol a server over SSH.
2. Enter the SSH host, port, login and one-time password, then the node ID,
   display name, region, public VPN domain and public addresses.
3. Verify the displayed SHA-256 host-key fingerprint before accepting it.
4. The master streams the pinned bundle, installs the isolated node, configures
   the inter-node credential and performs an authenticated NP/2 attestation.
   The node is published only after all steps pass.
5. Use `E` to enable/drain, `P` to publish/hide the server, `A` to grant or
   remove that selected node for one user credential, `S` to synchronize active
   user credentials and `D` to remove it.

`F4 ROUTES` manages administrator routes. Press `N` and complete the selector
wizard:

1. Enter a stable route ID, display name and priority (lower runs first).
2. Select `DOMAIN`, `IP/CIDR`, `GEOIP` or `GEOSITE`, then enter its value.
3. Select current/direct/auto/block or an enabled cluster egress node.
4. Select one or more active users with `Space` (`A` selects all), then press
   `Enter` and confirm creation.

`E` toggles the selected rule, `A` assigns an existing rule to one user and
`D` deletes it. A route assignment grants only the master and its selected
egress; `AUTO` intentionally grants all enabled nodes. GeoIP and GeoSite are
evaluated on the NP/2 server from checksum-verified V2Fly data installed under
`/etc/neproto/geodata`. On `F4 ROUTES`, press `G` to download, verify,
atomically activate, and hot-reload the same release on every reachable cluster
node. The result dialog lists every node and the short SHA-256 hashes it
activated. Press `T` to select the automatic `daily`, `weekly`, `monthly`, or
`off` schedule; verified weekly updates are enabled by default.

The equivalent non-interactive operations are:

```bash
neprotoctl geodata status --cluster=true
neprotoctl geodata update --cluster=true
neprotoctl geodata schedule --preset weekly
systemctl list-timers neproto-geodata-update.timer
```

The updater uses fixed V2Fly HTTPS release endpoints, verifies published
SHA-256 checksums, parses both databases before activation, and keeps the old
pair if any stage fails. The legacy `/usr/local/lib/neproto/update-geodata`
helper is retained for recovery but does not coordinate or hot-reload a
cluster. These rules remain server-authoritative
and are not weakened when an older catalogue-v1 client is used. Created
administrator routes apply to TCP and UDP. Mandatory administrator policy
always wins over a client-created route.

On `F2 USERS`, `C` toggles a user's cluster access. Only enabled,
client-published nodes assigned by policy enter that user's signed catalogue.
The iOS client verifies the catalogue signature and revision before displaying
servers or routes. Client CIDR/port/protocol rules apply to new flows; a
domain-only local rule uses the client's bounded DNS attribution; the server
still authoritatively verifies every administrator Domain/GeoSite rule.
The iOS diagnostics snapshot exposes only aggregate
`dns_attribution_queries`, `dns_attribution_responses`,
`dns_attribution_hits`, `dns_attribution_misses`,
`dns_attribution_cached`, `first_flight_domain_hits`, and
`first_flight_fallbacks` counters. A system-DNS GeoSite test should increase
the query/response counters and produce a DNS hit before the target stream is
opened. With application-owned encrypted DNS or a pre-existing OS cache,
`first_flight_domain_hits` must increase when the TCP/80 or TCP/443 first flight
contains a valid HTTP Host or TLS ClientHello SNI. A fallback increment means
the flow safely remained numeric; no domain, destination, payload, or SNI is
logged.

Before a production rollout, enrol a disposable node first, verify status in
`F3 CLUSTER`, switch a physical client between nodes, exercise one-hop and
two-hop TCP/UDP routes, revoke access, and complete the rollback drill from the
cluster release report.

## Rollback

Use `neprotoctl backup create` before configuration changes. Backups are
root-only and include installation state, service configuration, and per-user
credential files. ACME certificates remain in `/etc/letsencrypt`; rollback or
domain restore reselects and copies the matching certificate before restart.
To roll back:

1. Stop `neproto-server`.
2. Restore the prior binary, config, unit, and Caddyfile.
3. Run both configuration checks.
4. Reload systemd, restart the prior service, and reload Caddy.
5. Re-run listener, HTTPS, SSH, and Zabbix checks.

Remove only firewall rules that were added for this deployment. Never alter unrelated services as part of rollback.

## Incident containment

If the private path or secret may be exposed, stop NP/2, replace both routes, rotate the secret, validate, and restart. Authentication failures should be investigated through aggregate counters; do not add packet contents, destinations, route values, or key material to logs.
