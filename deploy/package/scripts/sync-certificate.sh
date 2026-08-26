#!/usr/bin/env bash
set -Eeuo pipefail

test_root=
if [[ -n ${NEPROTO_TEST_ROOT:-} ]]; then
  [[ ${NEPROTO_TEST_MODE:-} == 1 ]] || {
    printf 'NEPROTO_TEST_ROOT is only available in test mode\n' >&2
    exit 1
  }
  [[ $NEPROTO_TEST_ROOT == /* && -d $NEPROTO_TEST_ROOT ]] || {
    printf 'invalid NeProto test root\n' >&2
    exit 1
  }
  test_root=$(cd -- "$NEPROTO_TEST_ROOT" && pwd -P)
  [[ $test_root != / ]] || {
    printf 'invalid NeProto test root\n' >&2
    exit 1
  }
fi

installation=$test_root/etc/neproto/installation.json
[[ -f $installation ]] || { printf 'missing %s\n' "$installation" >&2; exit 1; }

domain=$(sed -n 's/.*"domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
mode=$(sed -n 's/.*"mode"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$installation")
service_gid=$(sed -n 's/.*"service_gid"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$installation")
[[ -n $domain && -n $mode && $service_gid =~ ^[0-9]+$ ]] || {
  printf 'invalid NeProto installation state\n' >&2
  exit 1
}

source_directory=$test_root/etc/letsencrypt/live/$domain
destination=$test_root/etc/neproto/tls
[[ -s $source_directory/fullchain.pem && -s $source_directory/privkey.pem ]] || {
  printf 'certificate for %s is unavailable\n' "$domain" >&2
  exit 1
}

install -d -o root -m 0750 "$destination"
chown 0:"$service_gid" "$destination"
copy_atomic() {
  local source=$1 target=$2 mode_value=$3 temporary
  temporary=$(mktemp "$destination/.certificate.XXXXXX")
  trap 'rm -f -- "$temporary"' RETURN
  install -o root -m "$mode_value" "$source" "$temporary"
  chown 0:"$service_gid" "$temporary"
  mv -f -- "$temporary" "$target"
  trap - RETURN
}
copy_atomic "$source_directory/fullchain.pem" "$destination/fullchain.pem" 0644
copy_atomic "$source_directory/privkey.pem" "$destination/privkey.pem" 0640

if [[ ${1:-} == --reload ]]; then
  if [[ $mode == bare-metal ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl try-restart neproto-server.service caddy.service >/dev/null
  elif [[ $mode == docker ]] && command -v docker >/dev/null 2>&1 && [[ -f /opt/neproto/compose.yml ]]; then
    (cd /opt/neproto && docker compose restart neproto caddy >/dev/null)
  fi
fi
