#!/usr/bin/env bash
set -Eeuo pipefail

repository=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(tr -d '[:space:]' <"$repository/VERSION")
expected="https://github.com/furylicouz/Neproto-SERVER/releases/download/$version/neproto-server-bundle-$version.tar.gz"

actual=$("$repository/install.sh" --print-release-url)
[[ $actual == "$expected" ]] || {
  printf 'unexpected release URL\nexpected: %s\nactual:   %s\n' "$expected" "$actual" >&2
  exit 1
}

"$repository/install.sh" --help | grep -q -- '--bootstrap-bundle'

standalone_dir=$(mktemp -d)
trap 'rm -rf -- "$standalone_dir"' EXIT
cp "$repository/install.sh" "$standalone_dir/install.sh"
standalone=$(
  cd "$standalone_dir"
  ./install.sh --print-release-url
)
[[ $standalone == "$expected" ]] || {
  printf 'standalone bootstrap depends on repository files\nexpected: %s\nactual:   %s\n' "$expected" "$standalone" >&2
  exit 1
}
printf 'PASS: root bootstrap contract\n'
