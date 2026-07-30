#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=${1:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}
web_dir=$package_dir/web

[[ -s $web_dir/server.js ]]
[[ -d $web_dir/.next/static ]]
[[ -s $web_dir/package.json ]]
[[ -s $web_dir/.next/BUILD_ID ]]

grep -Eq '"name"[[:space:]]*:[[:space:]]*"neproto-web"' "$web_dir/package.json"
printf 'PASS: standalone NeProto Web release payload\n'
