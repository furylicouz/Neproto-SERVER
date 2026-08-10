#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
"$package_dir/tests/platform-smoke.sh"
architecture=${1:-amd64}
expected_version=${2:-}
[[ -x $package_dir/bin/$architecture/neproto-server ]]
[[ -x $package_dir/bin/$architecture/neprotoctl ]]
[[ -x $package_dir/bin/$architecture/neproto-updater ]]
grep -Fq 'install-wizard --script' "$package_dir/install.sh"
if [[ -n $expected_version ]]; then
  [[ $("$package_dir/bin/$architecture/neproto-server" version) == "neproto-server $expected_version" ]]
  [[ $("$package_dir/bin/$architecture/neprotoctl" version) == "neprotoctl $expected_version" ]]
  [[ $(<"$package_dir/VERSION") == "$expected_version" ]]
fi
if [[ ! -x $package_dir/bin/$architecture/caddy ]]; then
  cp -- "$package_dir/bin/$architecture/neproto-server" "$package_dir/bin/$architecture/caddy"
fi
[[ -x $package_dir/bin/$architecture/node ]]
[[ -s $package_dir/web/server.js ]]

test_mode() {
  local mode=$1
  local root admin_secret_before
  root=$(mktemp -d)
  chmod 0755 "$root"
  trap 'rm -rf -- "$root"' RETURN

  local web_arguments=(--web-port 3100)
  if [[ $mode == bare-metal ]]; then
    web_arguments+=(--web-domain admin.example.com)
  fi
  NEPROTO_TEST_MODE=1 "$package_dir/install.sh" --root "$root" --mode "$mode" \
    --domain vpn.example.com --addresses 8.8.8.8 "${web_arguments[@]}" --non-interactive --skip-start

  local state=$root/etc/neproto/installation.json
  local first_https first_webrtc first_http3
  [[ -s $state && -s $root/etc/neproto/server.json && -s $root/etc/caddy/Caddyfile ]]
  [[ -s $root/etc/modules-load.d/neproto-bbr.conf ]]
  [[ -s $root/opt/neproto/web/server.js && -s $root/opt/neproto/web/.next/BUILD_ID ]]
  [[ -x $root/usr/local/lib/neproto/node ]]
  [[ -x $root/usr/local/bin/neproto-updater ]]
  grep -q '"web_enabled": true' "$state"
  grep -q '"web_port": 3100' "$state"
  grep -qx 'PORT=3100' "$root/etc/neproto/web.env"
  [[ -s $root/etc/neproto/web-admin.secret ]]
  admin_secret_before=$(<"$root/etc/neproto/web-admin.secret")
  [[ $(stat -c %a "$root/etc/neproto/web-admin.secret") == 640 ]]
  [[ -d $root/var/lib/neproto/update/inbox ]]
  [[ $(stat -c %a "$root/var/lib/neproto/update") == 2750 ]]
  [[ $(stat -c %g "$root/var/lib/neproto/update") == 65532 ]]
  [[ -s $root/etc/systemd/system/neproto-update.service ]]
  [[ -s $root/etc/systemd/system/neproto-update.path ]]
  [[ -s $root/etc/systemd/system/neproto-update-check.service ]]
  [[ -s $root/etc/systemd/system/neproto-update-check.path ]]
  [[ -s $root/etc/systemd/system/neproto-update-check.timer ]]
  [[ -s $root/etc/systemd/system/neproto-control.service ]]
  grep -q 'ExecStart=/usr/local/bin/neprotoctl web-api-server' "$root/etc/systemd/system/neproto-control.service"
  grep -q '^RuntimeDirectory=neproto$' "$root/etc/systemd/system/neproto-control.service"
  ! grep -q '__SERVICE_GID__' "$root/etc/systemd/system/neproto-control.service"
  ! grep -q '^Conflicts=' "$root/etc/systemd/system/neproto-update.service"
  ! grep -q '^Conflicts=' "$root/etc/systemd/system/neproto-update-check.service"
  [[ -s $root/opt/neproto/neproto-server-bundle.tar.gz ]]
  grep -qx 'tcp_bbr' "$root/etc/modules-load.d/neproto-bbr.conf"
  [[ -s $root/etc/sysctl.d/99-neproto-performance.conf ]]
  grep -qx 'net.core.default_qdisc=fq' "$root/etc/sysctl.d/99-neproto-performance.conf"
  grep -qx 'net.ipv4.tcp_congestion_control=bbr' "$root/etc/sysctl.d/99-neproto-performance.conf"
  grep -q '"require_datagrams": false' "$state"
  [[ -L $root/usr/local/bin/np && $(readlink "$root/usr/local/bin/np") == neprotoctl ]]
  [[ -s $root/etc/profile.d/neproto-console.sh ]]
  grep -Fq '[ -t 0 ] && [ -t 1 ]' "$root/etc/profile.d/neproto-console.sh"
  grep -Fq 'NEPROTO_NO_AUTO_UI' "$root/etc/profile.d/neproto-console.sh"
  grep -Fq 'SSH_TTY' "$root/etc/profile.d/neproto-console.sh"
  grep -Fq 'SSH_ORIGINAL_COMMAND' "$root/etc/profile.d/neproto-console.sh"
  grep -Fq '/usr/local/bin/np' "$root/etc/profile.d/neproto-console.sh"
  grep -q '"credential_directory": "/etc/neproto/users/active"' "$root/etc/neproto/server.json"
  grep -q '"user_policy_file": "/etc/neproto/users/index.json"' "$root/etc/neproto/server.json"
  grep -q '"usage_state_file": "/var/lib/neproto/usage/state.json"' "$root/etc/neproto/server.json"
  [[ -d $root/var/lib/neproto/usage ]]
  grep -q '"geodata_directory": "/etc/neproto/geodata"' "$root/etc/neproto/server.json"
  [[ -f $root/etc/neproto/geodata/geoip.dat && -f $root/etc/neproto/geodata/geosite.dat ]]
  [[ -x $root/usr/local/lib/neproto/update-geodata ]]
  [[ $(<"$root/etc/neproto/geodata-schedule") == weekly ]]
  [[ -s $root/etc/systemd/system/neproto-geodata-update.service ]]
  [[ -s $root/etc/systemd/system/neproto-geodata-update.timer ]]
  grep -q 'geodata update --cluster=true' "$root/etc/systemd/system/neproto-geodata-update.service"
  grep -q '"metrics_listen": "127.0.0.1:9464"' "$root/etc/neproto/server.json"
  grep -q '"enable_http3": true' "$root/etc/neproto/server.json"
  grep -q '"enable_webrtc_datagrams": true' "$root/etc/neproto/server.json"
  grep -q '"enable_http3_datagrams": true' "$root/etc/neproto/server.json"
  grep -q '"enable_constellation": true' "$root/etc/neproto/server.json"
  grep -q '"enable_forward_secrecy": true' "$root/etc/neproto/server.json"
  grep -q '"max_udp_associations_global": 10000' "$root/etc/neproto/server.json"
  grep -q '"target_creates_per_second_per_user": 2000' "$root/etc/neproto/server.json"
  [[ -s $root/etc/neproto/tls/fullchain.pem && -s $root/etc/neproto/tls/privkey.pem ]]
  grep -q 'vpn.example.com' "$root/etc/caddy/Caddyfile"
  first_https=$(sed -n 's/.*"https_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$state")
  first_webrtc=$(sed -n 's/.*"webrtc_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$state")
  first_http3=$(sed -n 's/.*"http3_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$state")
  [[ $first_https =~ ^/[a-f0-9]{48}$ && $first_webrtc =~ ^/[a-f0-9]{48}$ && $first_http3 =~ ^/[a-f0-9]{48}$ ]]
  [[ $first_https != "$first_webrtc" && $first_https != "$first_http3" && $first_webrtc != "$first_http3" ]]

  NEPROTO_TEST_MODE=1 "$package_dir/install.sh" --root "$root" --mode "$mode" \
    --domain vpn.example.com --addresses 8.8.8.8 "${web_arguments[@]}" --non-interactive --skip-start
  grep -q "\"https_path\": \"$first_https\"" "$state"
  grep -q "\"webrtc_path\": \"$first_webrtc\"" "$state"
  grep -q "\"http3_path\": \"$first_http3\"" "$state"
  [[ $(<"$root/etc/neproto/web-admin.secret") == "$admin_secret_before" ]]
  [[ $(stat -c %a "$root/var/backups/neproto") == 700 ]]
  find "$root/var/backups/neproto" -mindepth 2 -maxdepth 2 -name web-admin.secret -type f -print -quit | grep -q .
  for snapshot in "$root"/var/backups/neproto/*; do
    [[ ! -d $snapshot ]] || [[ $(stat -c %a "$snapshot") == 700 ]]
  done

  if [[ $mode == docker ]]; then
    [[ -s $root/opt/neproto/compose.yml && -s $root/opt/neproto/Dockerfile.neproto && -s $root/opt/neproto/Dockerfile.web ]]
    [[ -x $root/opt/neproto/node ]]
    grep -q 'network_mode: host' "$root/opt/neproto/compose.yml"
    grep -q 'image: neproto/web:local' "$root/opt/neproto/compose.yml"
    grep -q 'HOSTNAME: "0.0.0.0"' "$root/opt/neproto/compose.yml"
    grep -q 'PORT: "3100"' "$root/opt/neproto/compose.yml"
    grep -q '/var/lib/neproto/update:/var/lib/neproto/update:rw' "$root/opt/neproto/compose.yml"
    grep -q '/run/neproto:/run/neproto:ro' "$root/opt/neproto/compose.yml"
    grep -qx 'HOSTNAME=0.0.0.0' "$root/etc/neproto/web.env"
    grep -q 'cap_drop: \[ALL\]' "$root/opt/neproto/compose.yml"
    grep -q '/etc/neproto/geodata:/etc/neproto/geodata:rw' "$root/opt/neproto/compose.yml"
    grep -q '/var/lib/neproto/usage:/var/lib/neproto/usage:rw' "$root/opt/neproto/compose.yml"
    [[ $(grep -c 'cap_add: \[NET_BIND_SERVICE\]' "$root/opt/neproto/compose.yml") -eq 2 ]]
    if grep -q '__SERVICE_GID__' "$root/opt/neproto/compose.yml"; then
      return 1
    fi
  else
    [[ -s $root/etc/systemd/system/neproto-server.service && -s $root/etc/systemd/system/caddy.service && -s $root/etc/systemd/system/neproto-web.service ]]
    grep -q 'ExecStart=/usr/local/lib/neproto/node server.js' "$root/etc/systemd/system/neproto-web.service"
    grep -q '^Wants=.*neproto-control.service' "$root/etc/systemd/system/neproto-web.service"
    grep -q '^ReadWritePaths=/var/lib/neproto/update/inbox$' "$root/etc/systemd/system/neproto-web.service"
    grep -Fq '/api/auth/session' "$package_dir/install.sh"
    grep -Fq -- '--data-binary @-' "$package_dir/install.sh"
    grep -q 'admin.example.com' "$root/etc/caddy/Caddyfile"
    grep -q 'reverse_proxy 127.0.0.1:3100' "$root/etc/caddy/Caddyfile"
    grep -qx 'HOSTNAME=127.0.0.1' "$root/etc/neproto/web.env"
    grep -q 'NoNewPrivileges=true' "$root/etc/systemd/system/neproto-server.service"
    grep -q '^AmbientCapabilities=CAP_NET_BIND_SERVICE$' "$root/etc/systemd/system/neproto-server.service"
    grep -q '^CapabilityBoundingSet=CAP_NET_BIND_SERVICE$' "$root/etc/systemd/system/neproto-server.service"
    grep -q '^ReadWritePaths=/etc/neproto/users/active /etc/neproto/geodata /var/lib/neproto/usage$' "$root/etc/systemd/system/neproto-server.service"
  fi

  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user add --name "Smoke iPhone" --profile web --no-restart >"$root/add.out"
  local identifier
  identifier=$(sed -n 's/.*(\([^)]*\)).*/\1/p' "$root/add.out")
  [[ -n $identifier && -s $root/etc/neproto/users/active/$identifier.secret ]]
  chroot --userspec=65532:65532 "$root" /usr/local/bin/neproto-server check --config /etc/neproto/server.json \
    | grep -q 'server configuration OK'
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user export --id "$identifier" --format uri >"$root/profile.uri" 2>"$root/export.err"
  grep -q '^np2://import/v2/' "$root/profile.uri"
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user export --id "$identifier" --format json >"$root/profile.json" 2>>"$root/export.err"
  grep -q '"max_parallel_carriers": 3' "$root/profile.json"
  grep -q '"enable_constellation": true' "$root/profile.json"
  grep -q '"enable_forward_secrecy": true' "$root/profile.json"
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user export --id "$identifier" --format manual >"$root/profile.manual" 2>>"$root/export.err"
  grep -q '^Server: vpn.example.com$' "$root/profile.manual"
  grep -q "^Credential ID: $identifier$" "$root/profile.manual"
  grep -q '^Secret: ' "$root/profile.manual"
  grep -q '^Import URI: np2://import/v2/' "$root/profile.manual"
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user rotate --id "$identifier" >/dev/null
  if NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user revoke --id "$identifier" >"$root/revoke-last.out" 2>&1; then
    printf 'ERROR: sole active user was revoked\n' >&2
    return 1
  fi
  grep -q 'cannot revoke the last active NP/2 user' "$root/revoke-last.out"
  [[ -s $root/etc/neproto/users/active/$identifier.secret ]]
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user add --name "Smoke Service Keeper" --profile quiet >/dev/null
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user revoke --id "$identifier" >/dev/null
  [[ ! -e $root/etc/neproto/users/active/$identifier.secret ]]
  grep -q "$identifier" "$root/etc/neproto/users/index.json"
  NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/neprotoctl" user delete --id "$identifier" --confirm DELETE >/dev/null
  ! grep -q "$identifier" "$root/etc/neproto/users/index.json"
  if find "$root/etc/neproto/users/revoked" -maxdepth 1 -name "$identifier*.secret" -print -quit | grep -q .; then
    return 1
  fi
  printf '0\n' | NEPROTO_TEST_ROOT=$root "$root/usr/local/bin/np" >"$root/menu.out"
  grep -q 'NP/2 CONSTELLATION SERVER CONTROL' "$root/menu.out"
  grep -q 'SAFE STORAGE' "$root/menu.out"

  printf 'PASS: %s isolated install and lifecycle\n' "$mode"
}

test_mode bare-metal
test_mode docker

test_cluster_node() {
  local root bootstrap p1 p2 p3 p4 p5 p6 p7 p8 p9
  root=$(mktemp -d)
  bootstrap=$(mktemp)
  trap 'rm -rf -- "$root" "$bootstrap" "$bootstrap.retry" "$bootstrap.foreign"' RETURN
  chmod 0755 "$root"
  p1=/111111111111111111111111111111111111111111111111
  p2=/222222222222222222222222222222222222222222222222
  p3=/333333333333333333333333333333333333333333333333
  p4=/444444444444444444444444444444444444444444444444
  p5=/555555555555555555555555555555555555555555555555
  p6=/666666666666666666666666666666666666666666666666
  p7=/777777777777777777777777777777777777777777777777
  p8=/888888888888888888888888888888888888888888888888
  p9=/999999999999999999999999999999999999999999999999
  cat >"$bootstrap" <<EOF
{
  "version":1,"mode":"bare-metal","domain":"edge.example.com","addresses":["1.1.1.1"],"acme_email":"",
  "https_path":"$p1","webrtc_path":"$p2","http3_path":"$p3",
  "cluster_id":"np2-cluster","node_id":"edge-fi","name":"Finland Edge","region":"Helsinki",
  "roles":["ingress","relay","egress"],"master_node_id":"master",
  "master_domain":"vpn.example.com","master_addresses":["8.8.8.8"],
  "master_https_path":"$p4","master_webrtc_path":"$p5","master_http3_path":"$p6",
  "peer_credential_id":"AQEBAQEBAQEBAQEBAQEBAQ","peer_secret":"AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI"
}
EOF
  NEPROTO_TEST_MODE=1 "$package_dir/cluster-node-install.sh" --root "$root" --bootstrap "$bootstrap"
  grep -q '"cluster_node_id": "edge-fi"' "$root/etc/neproto/server.json"
  grep -q '"cluster_master_node_id": "master"' "$root/etc/neproto/server.json"
  grep -q '"node_id": "master"' "$root/etc/neproto/cluster/accepted-peers.json"
  grep -q 'vpn.example.com' "$root/etc/neproto/cluster/peers/master/client.json"
  grep -q '"enable_constellation": false' "$root/etc/neproto/cluster/peers/master/client.json"
  [[ -s $root/etc/neproto/users/active/AQEBAQEBAQEBAQEBAQEBAQ.secret ]]

  jq --arg https_path "$p7" --arg webrtc_path "$p8" --arg http3_path "$p9" \
    '.peer_credential_id = "AwMDAwMDAwMDAwMDAwMDAw" |
     .peer_secret = "BAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQ" |
     .https_path = $https_path | .webrtc_path = $webrtc_path | .http3_path = $http3_path' \
    "$bootstrap" >"$bootstrap.retry"
  mv -f "$bootstrap.retry" "$bootstrap"
  NEPROTO_TEST_MODE=1 "$package_dir/cluster-node-install.sh" --root "$root" --bootstrap "$bootstrap"
  [[ ! -e $root/etc/neproto/users/active/AQEBAQEBAQEBAQEBAQEBAQ.secret ]]
  [[ -s $root/etc/neproto/users/active/AwMDAwMDAwMDAwMDAwMDAw.secret ]]
  grep -q 'AwMDAwMDAwMDAwMDAwMDAw' "$root/etc/neproto/cluster/accepted-peers.json"
  grep -qx 'BAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQ' "$root/etc/neproto/cluster/peers/master/secret"
  grep -q "$p7" "$root/etc/neproto/installation.json"
  grep -q "$p8" "$root/etc/neproto/installation.json"
  grep -q "$p9" "$root/etc/neproto/installation.json"

  jq '.cluster_id = "different-cluster"' "$bootstrap" >"$bootstrap.foreign"
  if NEPROTO_TEST_MODE=1 "$package_dir/cluster-node-install.sh" --root "$root" --bootstrap "$bootstrap.foreign" \
    >"$root/foreign-cluster.out" 2>&1; then
    printf 'ERROR: foreign cluster bootstrap replaced an enrolled node\n' >&2
    return 1
  fi
  grep -q 'already belongs to a different cluster node' "$root/foreign-cluster.out"
  grep -q '"cluster_id": "np2-cluster"' "$root/etc/neproto/cluster/node.json"
  [[ -s $root/etc/neproto/users/active/AwMDAwMDAwMDAwMDAwMDAw.secret ]]
  chroot --userspec=65532:65532 "$root" /usr/local/bin/neproto-server check --config /etc/neproto/server.json \
    | grep -q 'server configuration OK'
  printf 'PASS: isolated cluster node bootstrap\n'
}

test_cluster_node
