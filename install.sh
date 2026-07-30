#!/usr/bin/env bash
set -Eeuo pipefail

repository_url=https://github.com/furylicouz/Neproto-SERVER
embedded_version=np2-0.5.1
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
version=${NEPROTO_VERSION:-}
if [[ -z $version && -f $script_dir/VERSION ]]; then
  version=$(tr -d '[:space:]' <"$script_dir/VERSION")
fi
[[ -n $version ]] || version=$embedded_version
[[ $version =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'ERROR: invalid VERSION: %s\n' "$version" >&2
  exit 2
}

asset="neproto-server-bundle-$version.tar.gz"
release_url="$repository_url/releases/download/$version/$asset"
bundle=
downloaded_bundle=false
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

for command in awk df tar sha256sum mktemp; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'ERROR: required command is unavailable: %s\n' "$command" >&2
    exit 1
  }
done

select_work_base() {
  local candidate available best= best_available=0
  if [[ -n ${NEPROTO_TMPDIR:-} ]]; then
    [[ $NEPROTO_TMPDIR == /* && -d $NEPROTO_TMPDIR && -w $NEPROTO_TMPDIR ]] || {
      printf 'ERROR: NEPROTO_TMPDIR must be an absolute writable directory\n' >&2
      exit 1
    }
    printf '%s\n' "$NEPROTO_TMPDIR"
    return
  fi
  for candidate in /var/tmp /tmp /root; do
    [[ -d $candidate && -w $candidate ]] || continue
    available=$(df -Pk "$candidate" | awk 'END {print $4}')
    [[ $available =~ ^[0-9]+$ ]] || continue
    if (( available > best_available )); then
      best=$candidate
      best_available=$available
    fi
  done
  [[ -n $best ]] || {
    printf 'ERROR: no writable bootstrap directory is available\n' >&2
    exit 1
  }
  printf '%s\n' "$best"
}

work_base=$(select_work_base)
available_kb=$(df -Pk "$work_base" | awk 'END {print $4}')
minimum_kb=2097152
[[ ${NEPROTO_BOOTSTRAP_TEST_MODE:-0} != 1 ]] || minimum_kb=1
if [[ ! $available_kb =~ ^[0-9]+$ || $available_kb -lt $minimum_kb ]]; then
  available_mb=$(( ${available_kb:-0} / 1024 ))
  printf 'ERROR: insufficient free space in %s: %s MiB available, at least 2048 MiB required.\n' \
    "$work_base" "$available_mb" >&2
  printf 'Free disk space or retry with NEPROTO_TMPDIR=/path/on/a/larger/filesystem.\n' >&2
  exit 1
fi

temporary=$(mktemp -d "$work_base/neproto-bootstrap.XXXXXX")
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT

if [[ -z $bundle ]]; then
  command -v curl >/dev/null 2>&1 || {
    printf 'ERROR: curl is required. Install curl and retry.\n' >&2
    exit 1
  }
  bundle=$temporary/$asset
  downloaded_bundle=true
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

if $downloaded_bundle; then
  rm -f -- "$bundle" "$bundle.sha256"
fi

"$installer" "${forwarded[@]}"
