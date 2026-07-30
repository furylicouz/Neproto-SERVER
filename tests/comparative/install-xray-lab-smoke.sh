#!/usr/bin/env bash
set -Eeuo pipefail

repository=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
source_directory=${1:?usage: install-xray-lab-smoke.sh SOURCE_DIRECTORY}
root=$(mktemp -d)
trap 'rm -rf -- "$root"' EXIT

bash "$repository/tests/comparative/install-xray-lab.sh" \
  --source "$source_directory" --root "$root" --skip-start

[[ -x $root/opt/neproto-comparative/bin/xray ]]
[[ -s $root/opt/neproto-comparative/config/vision-server.json ]]
[[ -s $root/opt/neproto-comparative/config/xhttp-server.json ]]
[[ -s $root/etc/systemd/system/xray-lab-vision.service ]]
[[ -s $root/etc/systemd/system/xray-lab-xhttp.service ]]
grep -Fq 'NoNewPrivileges=true' "$root/etc/systemd/system/xray-lab-vision.service"
grep -Fq 'CapabilityBoundingSet=' "$root/etc/systemd/system/xray-lab-vision.service"
grep -Fq 'TCP 18443' "$root/etc/systemd/system/xray-lab-vision.service"
grep -Fq 'TCP 18444' "$root/etc/systemd/system/xray-lab-xhttp.service"
if grep -R -qE '(:443|UDP 443|TCP 443)' "$root/etc/systemd/system"; then
  printf 'lab unit unexpectedly references production port 443\n' >&2
  exit 1
fi
[[ ! -e $root/etc/caddy && ! -e $root/etc/neproto ]]
printf 'PASS: isolated Xray comparative lab install\n'
