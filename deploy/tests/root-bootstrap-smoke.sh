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
printf 'PASS: root bootstrap contract\n'
