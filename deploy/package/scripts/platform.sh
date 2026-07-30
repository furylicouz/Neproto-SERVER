#!/usr/bin/env bash

NEPROTO_SUPPORTED_PLATFORMS='Ubuntu 22.04/24.04/26.04 and Debian 12/13'

neproto_detect_platform() {
  local release_file=${1:-/etc/os-release}
  local ID= VERSION_ID=
  [[ -r $release_file ]] || return 1
  # os-release is the systemd-defined shell-compatible platform contract.
  # shellcheck disable=SC1090
  source "$release_file"
  case "${ID:-}:${VERSION_ID:-}" in
    ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|debian:12|debian:13)
      printf '%s:%s\n' "$ID" "$VERSION_ID"
      ;;
    *)
      return 1
      ;;
  esac
}

neproto_compose_package() {
  local platform=${1:?platform is required}
  case "$platform" in
    # Debian 13 ships Compose v2 as the docker-compose package.
    # Source: https://packages.debian.org/trixie/docker-compose
    debian:13) printf 'docker-compose\n' ;;
    *) printf 'docker-compose-plugin\n' ;;
  esac
}
