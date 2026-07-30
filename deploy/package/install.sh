#!/usr/bin/env bash
set -Eeuo pipefail

mode=
domain=
addresses=
acme_email=
web_domain=
web_port=3000
requested_https_path=
requested_webrtc_path=
requested_http3_path=
root=/
non_interactive=false
skip_start=false
cluster_recovery=false
requested_interactive=false
(( $# == 0 )) && requested_interactive=true

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
info() { printf '[NeProto] %s\n' "$*"; }

usage() {
  cat <<'EOF'
Usage: ./install.sh [--mode docker|bare-metal] [--domain DOMAIN]
                    [--addresses IP[,IP...]] [--email ACME_EMAIL]
                    [--web-domain DOMAIN] [--web-port PORT]
                    [--https-path /HEX] [--webrtc-path /HEX]
                    [--http3-path /HEX]
                    [--non-interactive] [--skip-start]

With no arguments, starts the interactive NP/2 installation wizard.
EOF
}

while (($#)); do
  case "$1" in
    --mode) mode=${2-}; shift 2 ;;
    --domain) domain=${2-}; shift 2 ;;
    --addresses) addresses=${2-}; shift 2 ;;
    --email) acme_email=${2-}; shift 2 ;;
    --web-domain) web_domain=${2-}; shift 2 ;;
    --web-port) web_port=${2-}; shift 2 ;;
    --https-path) requested_https_path=${2-}; shift 2 ;;
    --webrtc-path) requested_webrtc_path=${2-}; shift 2 ;;
    --http3-path) requested_http3_path=${2-}; shift 2 ;;
    --non-interactive) non_interactive=true; shift ;;
    --skip-start) skip_start=true; shift ;;
    --cluster-recovery) cluster_recovery=true; shift ;;
    --root)
      [[ ${NEPROTO_TEST_MODE:-} == 1 ]] || die '--root is restricted to isolated tests'
      root=${2-}; shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
root=${root%/}
[[ -n $root ]] || root=/
path_in_root() { if [[ $root == / ]]; then printf '/%s' "${1#/}"; else printf '%s/%s' "$root" "${1#/}"; fi; }

platform_script=$script_dir/scripts/platform.sh
[[ -r $platform_script ]] || die 'missing platform compatibility definitions'
# shellcheck source=scripts/platform.sh
source "$platform_script"

if [[ ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  [[ $EUID -eq 0 ]] || die 'run the installer as root'
  [[ $(uname -s) == Linux ]] || die 'only Linux is supported'
  machine=$(uname -m)
  platform=$(neproto_detect_platform /etc/os-release) || \
    die "supported systems: $NEPROTO_SUPPORTED_PLATFORMS"
else
  machine=${NEPROTO_TEST_MACHINE:-x86_64}
  platform=${NEPROTO_TEST_PLATFORM:-debian:13}
fi

case "$machine" in
  x86_64) architecture=amd64 ;;
  aarch64) architecture=arm64 ;;
  *) die "unsupported architecture: $machine" ;;
esac

bundle_bin=$script_dir/bin/$architecture
[[ -x $bundle_bin/neproto-server ]] || die "missing $bundle_bin/neproto-server"
[[ -x $bundle_bin/neprotoctl ]] || die "missing $bundle_bin/neprotoctl"
[[ -x $bundle_bin/neproto-updater ]] || die "missing $bundle_bin/neproto-updater"
[[ -x $bundle_bin/caddy ]] || die "missing $bundle_bin/caddy"
[[ -x $bundle_bin/node ]] || die "missing $bundle_bin/node"
[[ -s $script_dir/web/server.js && -s $script_dir/web/.next/BUILD_ID ]] || die 'missing NeProto Web standalone payload'

if $requested_interactive && $non_interactive == false && \
   [[ ${NEPROTO_CLASSIC_INSTALL:-0} != 1 ]] && [[ -t 0 && -t 1 ]]; then
  exec "$bundle_bin/neprotoctl" install-wizard --script "$script_dir/install.sh"
fi

if [[ -z $mode && $non_interactive == false ]]; then
  printf 'Deployment mode [1=bare-metal, 2=docker]: '
  read -r selection
  case "$selection" in 1) mode=bare-metal ;; 2) mode=docker ;; *) die 'invalid selection' ;; esac
fi
[[ $mode == bare-metal || $mode == docker ]] || die '--mode must be bare-metal or docker'
if $cluster_recovery; then
  $non_interactive || die '--cluster-recovery requires non-interactive mode'
  [[ -n $requested_https_path && -n $requested_webrtc_path && -n $requested_http3_path ]] || \
    die '--cluster-recovery requires all transport paths'
fi

if [[ -z $domain && $non_interactive == false ]]; then
  printf 'Lowercase VPN domain: '
  read -r domain
fi
[[ $domain =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])$ && $domain == *.* && $domain != *..* ]] || die 'invalid lowercase DNS domain'
if [[ -z $web_domain && $non_interactive == false ]]; then
  printf 'Optional web admin domain (leave empty for public TCP port 3000): '
  read -r web_domain
fi
[[ -z $web_domain || ( $web_domain =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])$ && $web_domain == *.* && $web_domain != *..* ) ]] || die 'invalid lowercase web DNS domain'
[[ -z $web_domain || $web_domain != "$domain" ]] || die 'web domain must differ from the NP/2 server domain'
[[ $web_port =~ ^[0-9]+$ ]] || die 'web port must be numeric'
(( web_port >= 1024 && web_port <= 65535 && web_port != 9080 && web_port != 9464 && (web_port < 40000 || web_port > 40100) )) || \
  die 'web port must be 1024-65535 and not overlap NP/2 service ports'
if [[ -z $acme_email && $non_interactive == false ]]; then
  printf 'ACME expiry email (leave empty to continue without email): '
  read -r acme_email
fi
[[ -z $acme_email || $acme_email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || die 'invalid ACME email'

if [[ -z $addresses ]]; then
  if [[ ${NEPROTO_TEST_MODE:-} == 1 ]]; then
    die '--addresses is required in isolated test mode'
  fi
  addresses=$(getent ahosts "$domain" | awk '{print $1}' | sort -u | paste -sd, -)
fi
[[ -n $addresses ]] || die 'domain has no resolved address'
IFS=',' read -r -a address_list <<<"$addresses"
(( ${#address_list[@]} >= 1 && ${#address_list[@]} <= 8 )) || die 'provide 1-8 addresses'
for address in "${address_list[@]}"; do
  [[ $address =~ ^[0-9a-fA-F:.]+$ ]] || die "invalid address: $address"
done
if [[ -n $web_domain && ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  web_addresses=$(getent ahosts "$web_domain" | awk '{print $1}' | sort -u | paste -sd, -)
  [[ -n $web_addresses ]] || die 'web domain has no resolved address'
fi

if [[ ${NEPROTO_TEST_MODE:-} != 1 && ${NEPROTO_SELF_UPDATE:-0} != 1 ]]; then
  info 'installing system dependencies'
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates openssl qrencode curl certbot kmod procps jq
  if [[ $mode == docker ]]; then
    command -v docker >/dev/null 2>&1 || apt-get install -y --no-install-recommends docker.io
    if ! docker compose version >/dev/null 2>&1; then
      compose_package=$(neproto_compose_package "$platform")
      if ! apt-get install -y --no-install-recommends "$compose_package"; then
        compose_installed=false
        for compose_fallback in docker-compose-plugin docker-compose-v2 docker-compose; do
          [[ $compose_fallback != "$compose_package" ]] || continue
          if apt-get install -y --no-install-recommends "$compose_fallback"; then
            compose_installed=true
            break
          fi
        done
        $compose_installed || die 'Docker Compose v2 package is unavailable'
      fi
    fi
    docker compose version >/dev/null || die 'Docker Compose v2 is unavailable'
  fi
elif [[ ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  info 'validating dependencies already installed on this managed node'
  required_commands=(certbot curl getent install jq openssl qrencode systemctl)
  [[ $mode != docker ]] || required_commands+=(docker)
  for command_name in "${required_commands[@]}"; do
    command -v "$command_name" >/dev/null 2>&1 || die "managed update requires installed command: $command_name"
  done
  [[ $mode != docker ]] || docker compose version >/dev/null || die 'Docker Compose v2 is unavailable'
fi

etc_neproto=$(path_in_root /etc/neproto)
etc_caddy=$(path_in_root /etc/caddy)
opt_neproto=$(path_in_root /opt/neproto)
web_dir=$opt_neproto/web
bin_dir=$(path_in_root /usr/local/bin)
site_dir=$(path_in_root /var/www/neproto)
certbot_webroot=$(path_in_root /var/www/certbot)
backup_root=$(path_in_root /var/backups/neproto)
systemd_dir=$(path_in_root /etc/systemd/system)
modules_load_dir=$(path_in_root /etc/modules-load.d)
sysctl_dir=$(path_in_root /etc/sysctl.d)
lib_dir=$(path_in_root /usr/local/lib/neproto)
profile_dir=$(path_in_root /etc/profile.d)
update_dir=$(path_in_root /var/lib/neproto/update)
update_inbox=$update_dir/inbox
info 'preparing secure directories'
mkdir -p -- "$etc_neproto/users/active" "$etc_neproto/users/revoked" "$etc_neproto/tls" "$etc_neproto/geodata" "$etc_caddy" "$opt_neproto" "$bin_dir" "$lib_dir" "$site_dir" "$certbot_webroot/.well-known/acme-challenge" "$backup_root" "$systemd_dir" "$modules_load_dir" "$sysctl_dir" "$profile_dir" "$update_inbox"
chmod 0700 "$etc_neproto" "$etc_neproto/users" "$etc_neproto/users/revoked"

installation=$etc_neproto/installation.json
https_path=
webrtc_path=
http3_path=
if [[ -f $installation ]]; then
  old_mode=$(sed -n 's/.*"mode"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  old_domain=$(sed -n 's/.*"domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  old_web_domain=$(sed -n 's/.*"web_domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  old_web_port=$(sed -n 's/.*"web_port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$installation")
  https_path=$(sed -n 's/.*"https_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  webrtc_path=$(sed -n 's/.*"webrtc_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  http3_path=$(sed -n 's/.*"http3_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
  [[ $old_mode == "$mode" ]] || die 'changing deployment mode requires an explicit migration'
  [[ $old_domain == "$domain" ]] || die 'use neprotoctl domain set to change an installed domain'
  [[ -z $old_web_domain || $old_web_domain == "$web_domain" ]] || die 'changing the web domain requires an explicit migration'
  [[ -z $old_web_port || $old_web_port == "$web_port" ]] || die 'changing the web port requires an explicit migration'
fi
random_path() { printf '/'; od -An -N24 -tx1 /dev/urandom | tr -d ' \n'; }
for requested_path in "$requested_https_path" "$requested_webrtc_path" "$requested_http3_path"; do
  [[ -z $requested_path || $requested_path =~ ^/[a-f0-9]{48}$ ]] || die 'transport paths must be slash plus 48 lowercase hex characters'
done
if [[ -n $requested_https_path ]]; then
  $cluster_recovery || [[ -z $https_path || $https_path == "$requested_https_path" ]] || die 'installed HTTPS path differs from requested cluster path'
  https_path=$requested_https_path
fi
if [[ -n $requested_webrtc_path ]]; then
  $cluster_recovery || [[ -z $webrtc_path || $webrtc_path == "$requested_webrtc_path" ]] || die 'installed WebRTC path differs from requested cluster path'
  webrtc_path=$requested_webrtc_path
fi
if [[ -n $requested_http3_path ]]; then
  $cluster_recovery || [[ -z $http3_path || $http3_path == "$requested_http3_path" ]] || die 'installed HTTP/3 path differs from requested cluster path'
  http3_path=$requested_http3_path
fi
[[ $https_path =~ ^/[a-f0-9]{48}$ ]] || https_path=$(random_path)
[[ $webrtc_path =~ ^/[a-f0-9]{48}$ && $webrtc_path != "$https_path" ]] || webrtc_path=$(random_path)
[[ $http3_path =~ ^/[a-f0-9]{48}$ && $http3_path != "$https_path" && $http3_path != "$webrtc_path" ]] || http3_path=$(random_path)
[[ $https_path != "$webrtc_path" && $https_path != "$http3_path" && $webrtc_path != "$http3_path" ]] || die 'cluster transport paths must be distinct'

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup=$backup_root/$timestamp
mkdir -p -- "$backup"
for current in "$installation" "$etc_neproto/server.json" "$etc_neproto/web.env" "$etc_caddy/Caddyfile" "$opt_neproto/compose.yml"; do
  [[ ! -e $current ]] || cp -a -- "$current" "$backup/$(basename -- "$current")"
done
[[ ! -d $web_dir ]] || cp -a -- "$web_dir" "$backup/web"

service_uid=
service_gid=
if [[ ${NEPROTO_TEST_MODE:-} == 1 ]]; then
  service_uid=${NEPROTO_TEST_UID:-65532}
  service_gid=${NEPROTO_TEST_GID:-65532}
elif [[ $mode == bare-metal ]]; then
  getent group neproto >/dev/null || groupadd --system neproto
  getent passwd neproto >/dev/null || useradd --system --gid neproto --home-dir /nonexistent --shell /usr/sbin/nologin neproto
  getent passwd caddy >/dev/null || useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
  usermod -a -G neproto caddy
  service_uid=$(id -u neproto)
  service_gid=$(id -g neproto)
else
  service_uid=65532
  service_gid=65532
fi

web_stage=$(mktemp -d "$opt_neproto/.web.XXXXXX")
cleanup_web_stage() {
  [[ -z ${web_stage:-} || ! -e $web_stage ]] || rm -rf -- "$web_stage"
}
trap cleanup_web_stage EXIT
if ! cp -R --no-preserve=ownership,mode,timestamps -- "$script_dir/web/." "$web_stage/"; then
  die 'cannot stage NeProto Web payload'
fi
find "$web_stage" -type d -exec chmod 0755 {} +
find "$web_stage" -type f -exec chmod 0644 {} +
chown -R root:root "$web_stage"

install -m 0755 "$bundle_bin/neproto-server" "$bin_dir/neproto-server"
info 'installing runtime components'
install -m 0755 "$bundle_bin/neprotoctl" "$bin_dir/neprotoctl"
install -m 0755 "$bundle_bin/neproto-updater" "$bin_dir/neproto-updater"
ln -sfn -- neprotoctl "$bin_dir/np"
install -m 0644 "$script_dir/profile/neproto-console.sh" "$profile_dir/neproto-console.sh"
install -m 0755 "$bundle_bin/caddy" "$bin_dir/caddy"
install -m 0755 "$bundle_bin/node" "$lib_dir/node"
install -m 0755 "$script_dir/scripts/sync-certificate.sh" "$lib_dir/sync-certificate"
install -m 0755 "$script_dir/scripts/provision-certificate.sh" "$lib_dir/provision-certificate"
install -m 0755 "$script_dir/scripts/update-geodata.sh" "$lib_dir/update-geodata"
install -m 0644 "$script_dir/systemd/neproto-geodata-update.service" "$systemd_dir/neproto-geodata-update.service"
install -m 0644 "$script_dir/systemd/neproto-geodata-update.timer" "$systemd_dir/neproto-geodata-update.timer"
install -m 0644 "$script_dir/systemd/neproto-update.service" "$systemd_dir/neproto-update.service"
install -m 0644 "$script_dir/systemd/neproto-update.path" "$systemd_dir/neproto-update.path"
install -m 0644 "$script_dir/systemd/neproto-update-check.service" "$systemd_dir/neproto-update-check.service"
install -m 0644 "$script_dir/systemd/neproto-update-check.path" "$systemd_dir/neproto-update-check.path"
install -m 0644 "$script_dir/systemd/neproto-update-check.timer" "$systemd_dir/neproto-update-check.timer"
web_previous=$opt_neproto/.web.previous
rm -rf -- "$web_previous"
[[ ! -d $web_dir ]] || mv -- "$web_dir" "$web_previous"
if ! mv -- "$web_stage" "$web_dir"; then
  [[ ! -d $web_previous ]] || mv -- "$web_previous" "$web_dir"
  die 'cannot activate staged NeProto Web payload'
fi
web_stage=
trap - EXIT
rm -rf -- "$web_previous"
if [[ ! -f $etc_neproto/geodata-schedule ]]; then
  printf 'weekly\n' >"$etc_neproto/geodata-schedule"
fi
chmod 0644 "$etc_neproto/geodata-schedule"
install -m 0644 "$script_dir/site/index.html" "$site_dir/index.html"
install -m 0644 "$script_dir/performance/neproto-bbr.conf" "$modules_load_dir/neproto-bbr.conf"
install -m 0644 "$script_dir/performance/99-neproto-performance.conf" "$sysctl_dir/99-neproto-performance.conf"
# Keep a root-only self-contained bundle so the master can provision future
# cluster nodes without requiring a workstation or another download.
tar -czf "$opt_neproto/neproto-server-bundle.tar.gz.tmp" -C "$script_dir" .
chmod 0600 "$opt_neproto/neproto-server-bundle.tar.gz.tmp"
mv -f -- "$opt_neproto/neproto-server-bundle.tar.gz.tmp" "$opt_neproto/neproto-server-bundle.tar.gz"
if [[ ${NEPROTO_TEST_MODE:-} != 1 && ${NEPROTO_SELF_UPDATE:-0} != 1 ]]; then
  modprobe tcp_bbr >/dev/null 2>&1 || true
  if grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control; then
    sysctl -p "$sysctl_dir/99-neproto-performance.conf" >/dev/null
    info 'enabled BBR congestion control with fq queueing'
  else
    rm -f -- "$modules_load_dir/neproto-bbr.conf" "$sysctl_dir/99-neproto-performance.conf"
    info 'BBR is unavailable in the running kernel; retained the host TCP defaults'
  fi
fi
if [[ -n $acme_email ]]; then
  printf '%s\n' "$acme_email" >"$etc_neproto/acme-email"
  chmod 0600 "$etc_neproto/acme-email"
fi

info 'installing verified GeoIP and GeoSite routing data'
if [[ ${NEPROTO_TEST_MODE:-} == 1 ]]; then
  : >"$etc_neproto/geodata/geoip.dat"
  : >"$etc_neproto/geodata/geosite.dat"
  chmod 0640 "$etc_neproto/geodata/geoip.dat" "$etc_neproto/geodata/geosite.dat"
else
  "$lib_dir/update-geodata" "$etc_neproto/geodata"
fi

address_json=
for address in "${address_list[@]}"; do
  escaped=${address//\\/\\\\}; escaped=${escaped//\"/\\\"}
  [[ -z $address_json ]] || address_json+=,
  address_json+="\"$escaped\""
done

umask 077
info 'writing NP/2 configuration'
web_host=0.0.0.0
[[ -z $web_domain ]] || web_host=127.0.0.1
release_version=$(tr -d '[:space:]' <"$script_dir/VERSION")
[[ $release_version =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'invalid bundled release version'
web_admin_secret=$etc_neproto/web-admin.secret
if [[ ! -s $web_admin_secret ]]; then
  temporary_secret=$(mktemp "$etc_neproto/.web-admin.XXXXXX")
  openssl rand -hex 32 >"$temporary_secret"
  mv -f -- "$temporary_secret" "$web_admin_secret"
fi
chmod 0640 "$web_admin_secret"
temporary=$(mktemp "$etc_neproto/.installation.XXXXXX")
cat >"$temporary" <<EOF
{
  "version": 1,
  "mode": "$mode",
  "domain": "$domain",
  "server_addresses": [$address_json],
  "https_path": "$https_path",
  "webrtc_path": "$webrtc_path",
  "http3_path": "$http3_path",
  "require_datagrams": false,
  "enable_constellation": true,
  "enable_forward_secrecy": true,
  "web_enabled": true,
  "web_domain": "$web_domain",
  "web_port": $web_port,
  "service_uid": $service_uid,
  "service_gid": $service_gid
}
EOF
mv -f -- "$temporary" "$installation"
chmod 0600 "$installation"

cat >"$etc_neproto/web.env" <<EOF
HOSTNAME=$web_host
PORT=$web_port
NEPROTO_VERSION=$release_version
EOF
chmod 0640 "$etc_neproto/web.env"

cat >"$etc_neproto/server.json" <<EOF
{
  "server_identity": "$domain",
  "credential_directory": "/etc/neproto/users/active",
  "listen": "127.0.0.1:9080",
  "metrics_listen": "127.0.0.1:9464",
  "geodata_directory": "/etc/neproto/geodata",
  "https_path": "$https_path",
  "webrtc_path": "$webrtc_path",
  "enable_http3": true,
  "enable_webrtc_datagrams": true,
  "enable_http3_datagrams": true,
  "http3_listen": ":443",
  "http3_path": "$http3_path",
  "http3_cert_file": "/etc/neproto/tls/fullchain.pem",
  "http3_key_file": "/etc/neproto/tls/privkey.pem",
  "udp_port_min": 40000,
  "udp_port_max": 40100,
  "max_cover_overhead_percent": 20,
  "initial_window_bytes": 1048576,
  "max_streams": 256,
  "max_sessions": 64,
  "max_webrtc_peers": 64,
  "max_http3_sessions": 64,
  "max_target_connections": 256,
  "enable_constellation": true,
  "enable_forward_secrecy": true,
  "resource_limits": {
    "max_sessions_per_user": 8,
    "max_tcp_connections_global": 6000,
    "max_tcp_connections_per_user": 512,
    "max_udp_associations_global": 10000,
    "max_udp_associations_per_user": 1024,
    "udp_packets_per_second_global": 100000,
    "udp_packets_per_second_per_user": 20000,
    "udp_bytes_per_second_global": 268435456,
    "udp_bytes_per_second_per_user": 67108864,
    "dns_queries_per_second_global": 5000,
    "dns_queries_per_second_per_user": 500,
    "target_creates_per_second_global": 20000,
    "target_creates_per_second_per_user": 2000
  },
  "dial_timeout": "10s",
  "gather_timeout": "8s",
  "connect_timeout": "12s",
  "http3_handshake_timeout": "5s",
  "http3_idle_timeout": "45s",
  "shutdown_timeout": "10s"
}
EOF
chmod 0640 "$etc_neproto/server.json"

sed -e "s/__DOMAIN__/$domain/g" -e "s|__HTTPS_PATH__|$https_path|g" -e "s|__WEBRTC_PATH__|$webrtc_path|g" \
  "$script_dir/templates/Caddyfile" >"$etc_caddy/Caddyfile"
if [[ -n $web_domain ]]; then
  sed -e "s/__WEB_DOMAIN__/$web_domain/g" -e "s/__WEB_PORT__/$web_port/g" \
    "$script_dir/templates/Caddyfile.web-domain" >>"$etc_caddy/Caddyfile"
fi
chmod 0640 "$etc_caddy/Caddyfile"

if [[ $mode == bare-metal ]]; then
  install -m 0644 "$script_dir/systemd/neproto-server.service" "$(path_in_root /etc/systemd/system/neproto-server.service)"
  install -m 0644 "$script_dir/systemd/caddy.service" "$(path_in_root /etc/systemd/system/caddy.service)"
  install -m 0644 "$script_dir/systemd/neproto-web.service" "$(path_in_root /etc/systemd/system/neproto-web.service)"
else
  install -m 0644 "$script_dir/docker/Dockerfile.neproto" "$opt_neproto/Dockerfile.neproto"
  install -m 0644 "$script_dir/docker/Dockerfile.caddy" "$opt_neproto/Dockerfile.caddy"
  install -m 0644 "$script_dir/docker/Dockerfile.web" "$opt_neproto/Dockerfile.web"
  sed -e "s/__SERVICE_GID__/$service_gid/g" \
    -e "s/__WEB_HOST__/$web_host/g" -e "s/__WEB_PORT__/$web_port/g" \
    -e "s/__NEPROTO_VERSION__/$release_version/g" \
    "$script_dir/docker/compose.yml" >"$opt_neproto/compose.yml"
  chmod 0644 "$opt_neproto/compose.yml"
  install -m 0755 "$bundle_bin/neproto-server" "$opt_neproto/neproto-server"
  install -m 0755 "$bundle_bin/caddy" "$opt_neproto/caddy"
  install -m 0755 "$bundle_bin/node" "$opt_neproto/node"
fi

sed -e "s/__SERVICE_GID__/$service_gid/g" \
  "$script_dir/systemd/neproto-control.service" >"$systemd_dir/neproto-control.service"
chmod 0644 "$systemd_dir/neproto-control.service"

# The service owns credential files but not the configuration tree. Every
# parent directory must nevertheless be traversable by the service group.
chown root:"$service_gid" "$etc_neproto" "$etc_neproto/users" "$etc_neproto/server.json"
chown root:"$service_gid" "$etc_neproto/web.env"
chown root:"$service_gid" "$web_admin_secret"
chown -R root:"$service_gid" "$etc_neproto/geodata"
chown "$service_uid":"$service_gid" "$etc_neproto/users/active"
chmod 0750 "$etc_neproto"
chmod 0710 "$etc_neproto/users"
chmod 0700 "$etc_neproto/users/active"
chmod 0640 "$etc_neproto/server.json"
chmod 0640 "$etc_neproto/web.env"
chmod 0640 "$web_admin_secret"
chmod 2770 "$etc_neproto/geodata"
chmod 0660 "$etc_neproto/geodata/geoip.dat" "$etc_neproto/geodata/geosite.dat"
chown root:"$service_gid" "$etc_neproto/tls"
chmod 0750 "$etc_neproto/tls"
chown root:"$service_gid" "$update_dir"
chown "$service_uid":"$service_gid" "$update_inbox"
# The updater writes status files as root while the unprivileged web process
# reads them through the shared service group. setgid keeps every atomic temp
# file and rename in that group across future upgrades.
chmod 2750 "$update_dir"
chmod 0700 "$update_inbox"

provision_certificate() {
  local live_directory=/etc/letsencrypt/live/$domain
  local bootstrap_pid='' bootstrap_config='' bootstrap_log=''
  local certificate_required=false
  cleanup_bootstrap() {
    if [[ -n $bootstrap_pid ]]; then
      kill "$bootstrap_pid" 2>/dev/null || true
      wait "$bootstrap_pid" 2>/dev/null || true
    fi
    [[ -z $bootstrap_config ]] || rm -f -- "$bootstrap_config"
    [[ -z $bootstrap_log ]] || rm -f -- "$bootstrap_log"
  }

  if [[ ! -s $live_directory/fullchain.pem || ! -s $live_directory/privkey.pem ]]; then
    certificate_required=true
  elif [[ -n $web_domain ]] && ! openssl x509 -in "$live_directory/fullchain.pem" -noout -checkhost "$web_domain" >/dev/null 2>&1; then
    certificate_required=true
  fi
  if $certificate_required; then
    if [[ $mode == bare-metal ]]; then
      systemctl stop caddy.service 2>/dev/null || true
    elif [[ -f $opt_neproto/compose.yml ]]; then
      (cd "$opt_neproto" && docker compose stop caddy >/dev/null 2>&1) || true
    fi
    bootstrap_config=$(mktemp /tmp/neproto-caddy-bootstrap.XXXXXX)
    bootstrap_log=$(mktemp /tmp/neproto-caddy-bootstrap-log.XXXXXX)
    printf 'ready\n' >"$certbot_webroot/.well-known/acme-challenge/neproto-ready"
    bootstrap_labels="http://$domain"
    [[ -z $web_domain ]] || bootstrap_labels+=", http://$web_domain"
    cat >"$bootstrap_config" <<EOF
$bootstrap_labels {
  root * /var/www/certbot
  file_server
}
EOF
    "$bundle_bin/caddy" run --config "$bootstrap_config" --adapter caddyfile >"$bootstrap_log" 2>&1 &
    bootstrap_pid=$!
    local ready=false
    for _ in $(seq 1 50); do
      if curl -fsS -H "Host: $domain" http://127.0.0.1/.well-known/acme-challenge/neproto-ready | grep -q '^ready$'; then
        ready=true
        break
      fi
      kill -0 "$bootstrap_pid" 2>/dev/null || break
      sleep 0.1
    done
    if [[ $ready != true ]]; then
      cat "$bootstrap_log" >&2 || true
      cleanup_bootstrap
      die 'cannot bind TCP/80 for ACME HTTP-01; free port 80 and retry'
    fi
    local registration=(--register-unsafely-without-email)
    [[ -z $acme_email ]] || registration=(--email "$acme_email")
    local certificate_domains=(-d "$domain")
    [[ -z $web_domain ]] || certificate_domains+=(-d "$web_domain")
    if ! certbot certonly --non-interactive --agree-tos "${registration[@]}" \
      --expand --webroot --webroot-path /var/www/certbot --cert-name "$domain" "${certificate_domains[@]}"; then
      cleanup_bootstrap
      die 'ACME certificate issuance failed; verify DNS and inbound TCP/80'
    fi
    cleanup_bootstrap
  fi

  "$lib_dir/sync-certificate"
  install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
  install -m 0755 "$script_dir/scripts/renew-certificate-hook.sh" \
    /etc/letsencrypt/renewal-hooks/deploy/neproto-sync-certificate
}

if [[ ${NEPROTO_TEST_MODE:-} == 1 ]]; then
  printf '%s\n' 'test certificate' >"$etc_neproto/tls/fullchain.pem"
  printf '%s\n' 'test private key' >"$etc_neproto/tls/privkey.pem"
  chown root:"$service_gid" "$etc_neproto/tls/fullchain.pem" "$etc_neproto/tls/privkey.pem"
  chmod 0644 "$etc_neproto/tls/fullchain.pem"
  chmod 0640 "$etc_neproto/tls/privkey.pem"
else
  info 'provisioning TLS certificate'
  provision_certificate
fi

if [[ ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  chown -R root:root "$etc_neproto/users/revoked"
  if [[ $mode == bare-metal ]]; then
    caddy_gid=$(id -g caddy)
    chown root:"$caddy_gid" "$etc_caddy/Caddyfile"
    install -d -o caddy -g caddy -m 0750 /var/lib/caddy
  else
    chown root:65533 "$etc_caddy/Caddyfile"
    install -d -o 65533 -g 65533 -m 0750 /var/lib/caddy
  fi
  if ! find "$etc_neproto/users/active" -maxdepth 1 -name '*.secret' -print -quit | grep -q .; then
    "$bin_dir/neprotoctl" user add --name Administrator --profile web --no-restart
  fi
  if [[ $mode == bare-metal ]]; then
    "$bin_dir/neproto-server" check --config "$etc_neproto/server.json"
    "$lib_dir/node" --check "$web_dir/server.js"
    "$bin_dir/caddy" validate --config "$etc_caddy/Caddyfile" --adapter caddyfile
    systemctl daemon-reload
    systemctl enable neproto-control.service neproto-server.service neproto-web.service caddy.service
    info 'starting services'
    $skip_start || systemctl restart neproto-control.service neproto-server.service neproto-web.service caddy.service
  else
    systemctl daemon-reload
    systemctl enable neproto-control.service
    $skip_start || systemctl restart neproto-control.service
    (cd "$opt_neproto" && docker compose config >/dev/null && docker compose build)
    info 'starting services'
    $skip_start || (cd "$opt_neproto" && docker compose up -d)
  fi
fi

if [[ ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  systemctl daemon-reload
  systemctl enable neproto-geodata-update.timer neproto-update.path neproto-update-check.path neproto-update-check.timer
  $skip_start || systemctl restart neproto-geodata-update.timer neproto-update.path neproto-update-check.path neproto-update-check.timer
  if ! $skip_start && [[ ${NEPROTO_SELF_UPDATE:-0} != 1 ]] && ! "$bin_dir/neproto-updater" check; then
    info 'initial GitHub update check failed; the scheduled checker will retry'
  fi
fi

if [[ ${NEPROTO_TEST_MODE:-} != 1 ]] && ! $skip_start; then
  info 'running final health checks'
  curl --fail --silent --show-error --unix-socket /run/neproto/control.sock http://localhost/v1/overview | \
    jq -e '.version != null and .services != null' >/dev/null || die 'NeProto control API health check failed'
  "$bin_dir/neprotoctl" doctor
  web_health="http://127.0.0.1:$web_port/api/health"
  [[ -z $web_domain ]] || web_health="https://$web_domain/api/health"
  curl --fail --silent --show-error "$web_health" | jq -e '.service == "neproto-web" and .status == "ok"' >/dev/null || \
    die 'NeProto Web health check failed'
fi

info "installation prepared in $mode mode"
info "backup: $backup"
info "server control panel: np"
info "web administrator secret: sudo cat /etc/neproto/web-admin.secret"
if [[ -n $web_domain ]]; then
  info "web admin: https://$web_domain"
else
  info "web admin: http://<server-ip>:$web_port"
fi
info "automation: neprotoctl doctor | neprotoctl user add/list/export/rotate/revoke/delete"
