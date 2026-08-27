#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=../scripts/platform.sh
source "$package_dir/scripts/platform.sh"

fixture=$(mktemp)
trap 'rm -f -- "$fixture"' EXIT

assert_supported() {
  local identifier=$1 version=$2 expected=$3 actual
  cat >"$fixture" <<EOF
ID=$identifier
VERSION_ID="$version"
EOF
  actual=$(neproto_detect_platform "$fixture")
  [[ $actual == "$expected" ]] || {
    printf 'unexpected platform result for %s %s: %s\n' "$identifier" "$version" "$actual" >&2
    return 1
  }
}

assert_unsupported() {
  local identifier=$1 version=$2
  cat >"$fixture" <<EOF
ID=$identifier
VERSION_ID="$version"
EOF
  if neproto_detect_platform "$fixture" >/dev/null 2>&1; then
    printf 'unsupported platform was accepted: %s %s\n' "$identifier" "$version" >&2
    return 1
  fi
}

assert_supported ubuntu 22.04 ubuntu:22.04
assert_supported ubuntu 24.04 ubuntu:24.04
assert_supported ubuntu 26.04 ubuntu:26.04
assert_supported debian 12 debian:12
assert_supported debian 13 debian:13
assert_unsupported debian 11
assert_unsupported ubuntu 25.10

[[ $(neproto_compose_package debian:13) == docker-compose ]]
[[ $(neproto_compose_package debian:12) == docker-compose-plugin ]]
[[ $(neproto_compose_package ubuntu:24.04) == docker-compose-plugin ]]

udp_sysctl=$package_dir/performance/90-neproto-udp.conf
udp_unit=$package_dir/systemd/neproto-udp-buffers.service
server_unit=$package_dir/systemd/neproto-server.service
update_unit=$package_dir/systemd/neproto-update.service
installer=$package_dir/install.sh
[[ -s $udp_sysctl ]]
grep -qx 'net.core.rmem_max=7500000' "$udp_sysctl"
grep -qx 'net.core.wmem_max=7500000' "$udp_sysctl"
[[ -s $udp_unit ]]
grep -qx 'ExecStart=/usr/sbin/sysctl -p /etc/sysctl.d/90-neproto-udp.conf' "$udp_unit"
grep -q '^Before=neproto-server.service$' "$udp_unit"
grep -q '^CapabilityBoundingSet=CAP_NET_ADMIN CAP_SYS_ADMIN$' "$udp_unit"
! grep -q '^ConditionPathExists=' "$udp_unit"
grep -q '^Requires=neproto-udp-buffers.service$' "$server_unit"
grep -q '^After=.*neproto-udp-buffers.service' "$server_unit"
grep -qx 'Environment=DOCKER_CONFIG=/var/lib/neproto/update/docker-config' "$update_unit"
grep -Fq 'neproto-udp-buffers.service' "$installer"
grep -Fq '90-neproto-udp.conf' "$installer"
grep -q 'required_commands=.*sysctl' "$installer"

printf 'PASS: supported platform matrix\n'
