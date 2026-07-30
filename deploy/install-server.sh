#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: install-server.sh --domain DOMAIN --server-binary PATH --caddy-binary PATH [--start]

Installs or upgrades a single NP/2 node. Existing routes and the pre-shared
secret are preserved. The script never changes firewall, SSH, or Zabbix state.
EOF
  exit 2
}

domain=
server_binary=
caddy_binary=
start_services=false
secret_tmp=
server_tmp=
client_tmp=
caddy_tmp=

cleanup() {
  rm -f -- "$secret_tmp" "$server_tmp" "$client_tmp" "$caddy_tmp"
}
trap cleanup EXIT

while (($#)); do
  case "$1" in
    --domain)
      (($# >= 2)) || usage
      domain=$2
      shift 2
      ;;
    --server-binary)
      (($# >= 2)) || usage
      server_binary=$2
      shift 2
      ;;
    --caddy-binary)
      (($# >= 2)) || usage
      caddy_binary=$2
      shift 2
      ;;
    --start)
      start_services=true
      shift
      ;;
    *) usage ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "install-server.sh must run as root" >&2; exit 1; }
[[ -n $domain && -n $server_binary && -n $caddy_binary ]] || usage
[[ $domain =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])$ && $domain == *.* && $domain != *..* ]] || {
  echo "invalid lowercase DNS domain" >&2
  exit 1
}
[[ -x $server_binary ]] || { echo "server binary is not executable" >&2; exit 1; }
[[ -x $caddy_binary ]] || { echo "Caddy binary is not executable" >&2; exit 1; }

server_version=$($server_binary version)
caddy_version=$($caddy_binary version)
[[ $server_version == neproto-server\ np2-* ]] || { echo "unexpected server build: $server_version" >&2; exit 1; }
[[ $caddy_version == v2.11.4\ * ]] || { echo "expected Caddy v2.11.4, got: $caddy_version" >&2; exit 1; }

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd -- "$script_dir/.." && pwd)
server_template=$project_root/deploy/examples/server.json.example
client_template=$project_root/deploy/examples/client.json.example
caddy_template=$project_root/deploy/caddy/Caddyfile.example
site_source=$project_root/deploy/caddy/site/index.html
server_unit=$project_root/deploy/systemd/neproto-server.service
caddy_unit=$project_root/deploy/systemd/caddy.service
operations_doc=$project_root/docs/OPERATIONS.md

for required in "$server_template" "$client_template" "$caddy_template" "$site_source" \
  "$server_unit" "$caddy_unit" "$operations_doc"; do
  [[ -f $required ]] || { echo "missing deployment artifact: $required" >&2; exit 1; }
done

existing_config=/etc/neproto/server.json
if [[ -f $existing_config ]]; then
  existing_domain=$(sed -n 's/^[[:space:]]*"server_identity":[[:space:]]*"\([^"]*\)",*$/\1/p' "$existing_config")
  https_path=$(sed -n 's/^[[:space:]]*"https_path":[[:space:]]*"\([^"]*\)",*$/\1/p' "$existing_config")
  webrtc_path=$(sed -n 's/^[[:space:]]*"webrtc_path":[[:space:]]*"\([^"]*\)",*$/\1/p' "$existing_config")
  [[ $existing_domain == "$domain" ]] || {
    echo "existing node identity differs; migrate explicitly instead of overwriting" >&2
    exit 1
  }
  [[ $https_path =~ ^/[A-Za-z0-9_-]{32,128}$ && $webrtc_path =~ ^/[A-Za-z0-9_-]{32,128}$ && $https_path != "$webrtc_path" ]] || {
    echo "existing private routes are malformed" >&2
    exit 1
  }
else
  command -v openssl >/dev/null || { echo "openssl is required to generate routes" >&2; exit 1; }
  https_path=/$(openssl rand -hex 24)
  webrtc_path=/$(openssl rand -hex 24)
fi

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir=/var/backups/neproto/$timestamp
install -d -o root -g root -m 0700 "$backup_dir"
for current in /usr/local/bin/neproto-server /usr/local/bin/caddy \
  /etc/neproto/server.json /etc/caddy/Caddyfile \
  /etc/systemd/system/neproto-server.service /etc/systemd/system/caddy.service; do
  if [[ -e $current ]]; then
    cp -a -- "$current" "$backup_dir/$(basename -- "$current")"
  fi
done

getent passwd neproto >/dev/null || useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin neproto
getent passwd caddy >/dev/null || useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy

install -o root -g root -m 0755 "$server_binary" /usr/local/bin/neproto-server
install -o root -g root -m 0755 "$caddy_binary" /usr/local/bin/caddy
install -d -o root -g neproto -m 0750 /etc/neproto
install -d -o root -g caddy -m 0750 /etc/caddy
install -d -o caddy -g caddy -m 0750 /var/lib/caddy
install -d -o root -g root -m 0755 /usr/share/doc/neproto /var/www/neproto
install -o root -g root -m 0644 "$operations_doc" /usr/share/doc/neproto/OPERATIONS.md
install -o root -g root -m 0644 "$site_source" /var/www/neproto/index.html

if [[ ! -e /etc/neproto/server.secret ]]; then
  secret_tmp=$(mktemp /etc/neproto/.server.secret.XXXXXX)
  umask 077
  /usr/local/bin/neproto-server generate-secret > "$secret_tmp"
  install -o neproto -g neproto -m 0600 "$secret_tmp" /etc/neproto/server.secret
else
  chown neproto:neproto /etc/neproto/server.secret
  chmod 0600 /etc/neproto/server.secret
fi

server_tmp=$(mktemp)
client_tmp=$(mktemp)
caddy_tmp=$(mktemp)
sed -e "s/vpn\.example\.com/$domain/g" \
  -e "s|/replace-with-random-https-route|$https_path|g" \
  -e "s|/replace-with-random-webrtc-route|$webrtc_path|g" \
  "$server_template" > "$server_tmp"
sed -e "s/vpn\.example\.com/$domain/g" \
  -e 's|"/etc/neproto/client.secret"|"client.secret"|' \
  -e "s|/replace-with-random-https-route|$https_path|g" \
  -e "s|/replace-with-random-webrtc-route|$webrtc_path|g" \
  "$client_template" > "$client_tmp"
sed -e "s/vpn\.example\.com/$domain/g" \
  -e "s|/replace-with-random-https-route|$https_path|g" \
  -e "s|/replace-with-random-webrtc-route|$webrtc_path|g" \
  "$caddy_template" > "$caddy_tmp"

install -o root -g neproto -m 0640 "$server_tmp" /etc/neproto/server.json
install -o root -g root -m 0600 "$client_tmp" /root/neproto-client.json
install -o root -g caddy -m 0640 "$caddy_tmp" /etc/caddy/Caddyfile
install -o root -g root -m 0644 "$server_unit" /etc/systemd/system/neproto-server.service
install -o root -g root -m 0644 "$caddy_unit" /etc/systemd/system/caddy.service

runuser -u neproto -- /usr/local/bin/neproto-server check --config /etc/neproto/server.json
runuser -u caddy -- env HOME=/var/lib/caddy /usr/local/bin/caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl daemon-reload
systemd-analyze verify /etc/systemd/system/neproto-server.service /etc/systemd/system/caddy.service

if $start_services; then
  systemctl enable neproto-server.service caddy.service
  systemctl restart neproto-server.service
  systemctl restart caddy.service
  systemctl is-active --quiet neproto-server.service caddy.service
else
  echo "validated installation prepared; rerun with --start to enable and start services"
fi

echo "NP/2 node installed; backup: $backup_dir"
