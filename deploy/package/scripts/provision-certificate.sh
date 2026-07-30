#!/usr/bin/env bash
set -Eeuo pipefail

domain=
while (($#)); do
  case "$1" in
    --domain) domain=${2-}; shift 2 ;;
    *) printf 'usage: provision-certificate --domain DOMAIN\n' >&2; exit 2 ;;
  esac
done
[[ $EUID -eq 0 ]] || { printf 'run as root\n' >&2; exit 1; }
[[ $domain =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])$ && $domain == *.* && $domain != *..* ]] || {
  printf 'invalid domain\n' >&2
  exit 1
}

installation=/etc/neproto/installation.json
configured=$(sed -n 's/.*"domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
mode=$(sed -n 's/.*"mode"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
web_domain=$(sed -n 's/.*"web_domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
[[ $configured == "$domain" && ( $mode == bare-metal || $mode == docker ) ]] || {
  printf 'domain does not match NeProto installation state\n' >&2
  exit 1
}

live_directory=/etc/letsencrypt/live/$domain
webroot=/var/www/certbot
bootstrap_pid=
bootstrap_config=
bootstrap_log=
cleanup() {
  if [[ -n $bootstrap_pid ]]; then
    kill "$bootstrap_pid" 2>/dev/null || true
    wait "$bootstrap_pid" 2>/dev/null || true
  fi
  [[ -z $bootstrap_config ]] || rm -f -- "$bootstrap_config"
  [[ -z $bootstrap_log ]] || rm -f -- "$bootstrap_log"
}
trap cleanup EXIT

certificate_required=false
if [[ ! -s $live_directory/fullchain.pem || ! -s $live_directory/privkey.pem ]]; then
  certificate_required=true
elif [[ -n $web_domain ]] && ! openssl x509 -in "$live_directory/fullchain.pem" -noout -checkhost "$web_domain" >/dev/null 2>&1; then
  certificate_required=true
fi
if $certificate_required; then
  if [[ $mode == bare-metal ]]; then
    systemctl stop caddy.service 2>/dev/null || true
  elif [[ -f /opt/neproto/compose.yml ]]; then
    (cd /opt/neproto && docker compose stop caddy >/dev/null 2>&1) || true
  fi
  install -d -m 0755 "$webroot/.well-known/acme-challenge"
  printf 'ready\n' >"$webroot/.well-known/acme-challenge/neproto-ready"
  bootstrap_config=$(mktemp /tmp/neproto-caddy-bootstrap.XXXXXX)
  bootstrap_log=$(mktemp /tmp/neproto-caddy-bootstrap-log.XXXXXX)
  bootstrap_labels="http://$domain"
  [[ -z $web_domain ]] || bootstrap_labels+=", http://$web_domain"
  cat >"$bootstrap_config" <<EOF
$bootstrap_labels {
  root * $webroot
  file_server
}
EOF
  /usr/local/bin/caddy run --config "$bootstrap_config" --adapter caddyfile >"$bootstrap_log" 2>&1 &
  bootstrap_pid=$!
  ready=false
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
    printf 'cannot bind TCP/80 for ACME HTTP-01\n' >&2
    exit 1
  fi
  registration=(--register-unsafely-without-email)
  if [[ -s /etc/neproto/acme-email ]]; then
    email=$(tr -d '\r\n' </etc/neproto/acme-email)
    [[ -z $email ]] || registration=(--email "$email")
  fi
  certificate_domains=(-d "$domain")
  [[ -z $web_domain ]] || certificate_domains+=(-d "$web_domain")
  certbot certonly --non-interactive --agree-tos "${registration[@]}" \
    --expand --webroot --webroot-path "$webroot" --cert-name "$domain" "${certificate_domains[@]}"
  cleanup
  bootstrap_pid=
  bootstrap_config=
  bootstrap_log=
fi

/usr/local/lib/neproto/sync-certificate
