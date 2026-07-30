#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
architecture=${1:-amd64}
terminal_log=$(mktemp)
terminal_output=$(mktemp)
trap 'rm -f -- "$terminal_log" "$terminal_output"' EXIT

set +e
(
  TERM=xterm-256color timeout --kill-after=1s 2s script --quiet --return \
    --command "stty cols 132 rows 42; exec $package_dir/bin/$architecture/neprotoctl install-wizard --script $package_dir/install.sh" \
    "$terminal_log" </dev/null >"$terminal_output" 2>&1
) 2>/dev/null
code=$?
set -e

[[ $code -eq 124 || $code -eq 137 ]]
for expected in 'CONSTELLATION DEPLOYMENT' 'INSTALLATION MATRIX' 'NETWORK MAP'; do
  if ! grep -aFq "$expected" "$terminal_log"; then
    printf 'missing installer TUI marker: %s\n' "$expected" >&2
    sed -n '1,80p' "$terminal_output" >&2 || true
    sed -n '1,80p' "$terminal_log" >&2 || true
    exit 1
  fi
done
printf 'PASS: cinematic installer TUI render\n'
