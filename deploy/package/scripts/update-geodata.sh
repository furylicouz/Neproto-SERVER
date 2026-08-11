#!/usr/bin/env bash
set -Eeuo pipefail

target=${1:-/etc/neproto/geodata}
[[ $target == /* ]] || {
  printf 'geodata target must be absolute\n' >&2
  exit 2
}
[[ ! -L $target ]] || {
  printf 'geodata target must not be a symlink\n' >&2
  exit 2
}
prepare_only=${NEPROTO_GEODATA_PREPARE_ONLY:-0}
if [[ $prepare_only == 1 && ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  printf 'prepare-only geodata mode is restricted to isolated tests\n' >&2
  exit 2
fi

geoip_url=https://github.com/v2fly/geoip/releases/latest/download/geoip.dat
geoip_sum_url=https://github.com/v2fly/geoip/releases/latest/download/geoip.dat.sha256sum
geosite_url=https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat
geosite_sum_url=https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat.sha256sum

temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT

download_verified() {
  local name=$1 url=$2 sum_url=$3 destination=$4 expected actual
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 --connect-timeout 15 --max-time 180 \
    -o "$temporary/$name" "$url"
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 --connect-timeout 15 --max-time 60 \
    -o "$temporary/$name.sha256sum" "$sum_url"
  expected=$(awk 'NR==1 {print tolower($1)}' "$temporary/$name.sha256sum")
  [[ $expected =~ ^[a-f0-9]{64}$ ]] || {
    printf 'invalid checksum response for %s\n' "$name" >&2
    exit 1
  }
  actual=$(sha256sum "$temporary/$name" | awk '{print tolower($1)}')
  [[ $actual == "$expected" ]] || {
    printf 'checksum mismatch for %s\n' "$name" >&2
    exit 1
  }
  install -m 0640 "$temporary/$name" "$destination.tmp"
  mv -f -- "$destination.tmp" "$destination"
}

[[ -d $target ]] || install -d -m 0750 "$target"
if [[ $prepare_only == 1 ]]; then
  printf 'NP/2 geodata directory prepared: %s\n' "$target"
  exit 0
fi
download_verified geoip.dat "$geoip_url" "$geoip_sum_url" "$target/geoip.dat"
download_verified dlc.dat "$geosite_url" "$geosite_sum_url" "$target/geosite.dat"
printf 'NP/2 geodata updated: %s\n' "$target"
