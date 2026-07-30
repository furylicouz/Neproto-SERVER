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

printf 'PASS: supported platform matrix\n'
