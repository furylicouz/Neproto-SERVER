#!/usr/bin/env bash
set -Eeuo pipefail

repository_url=https://github.com/furylicouz/Neproto-SERVER
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
version=$(tr -d '[:space:]' <"$script_dir/VERSION")
[[ $version =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'ERROR: invalid VERSION: %s\n' "$version" >&2
  exit 2
}

asset="neproto-server-bundle-$version.tar.gz"
release_url="$repository_url/releases/download/$version/$asset"
bundle=
forwarded=()

usage() {
  cat <<EOF
NeProto Server quick setup

Usage:
  sudo ./install.sh [NeProto installer options]
  sudo ./install.sh --bootstrap-bundle /path/to/$asset [options]

The default mode downloads the checksum-verified $version release bundle from:
  $release_url

Bootstrap options:
  --bootstrap-bundle PATH  Use a local bundle and its PATH.sha256 file.
  --print-release-url      Print the selected immutable release URL.
  -h, --help               Show this help.

All other options are passed to the versioned server installer. With no
options the full-screen NeProto setup wizard starts.
EOF
}

while (($#)); do
  case "$1" in
    --bootstrap-bundle)
      [[ $# -ge 2 ]] || { printf 'ERROR: --bootstrap-bundle requires a path\n' >&2; exit 2; }
      bundle=$2
      shift 2
      ;;
    --print-release-url)
      printf '%s\n' "$release_url"
      exit 0
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      forwarded+=("$1")
      shift
      ;;
  esac
done

for command in tar sha256sum mktemp; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'ERROR: required command is unavailable: %s\n' "$command" >&2
    exit 1
  }
done

temporary=$(mktemp -d)
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT

if [[ -z $bundle ]]; then
  command -v curl >/dev/null 2>&1 || {
    printf 'ERROR: curl is required. Install curl and retry.\n' >&2
    exit 1
  }
  bundle=$temporary/$asset
  printf '[NeProto] Downloading %s\n' "$release_url"
  curl --fail --location --proto '=https' --tlsv1.2 \
    --output "$bundle" "$release_url"
  curl --fail --location --proto '=https' --tlsv1.2 \
    --output "$bundle.sha256" "$release_url.sha256"
else
  bundle=$(cd -- "$(dirname -- "$bundle")" && pwd)/$(basename -- "$bundle")
fi

[[ -s $bundle ]] || { printf 'ERROR: bundle not found: %s\n' "$bundle" >&2; exit 1; }
[[ -s $bundle.sha256 ]] || { printf 'ERROR: checksum not found: %s.sha256\n' "$bundle" >&2; exit 1; }

printf '[NeProto] Verifying release checksum\n'
(
  cd -- "$(dirname -- "$bundle")"
  sha256sum --check --strict "$(basename -- "$bundle").sha256"
)

tar -xzf "$bundle" -C "$temporary"
installer=$temporary/neproto-server-bundle-$version/install.sh
[[ -x $installer ]] || {
  printf 'ERROR: verified release does not contain the expected installer\n' >&2
  exit 1
}

exec "$installer" "${forwarded[@]}"
