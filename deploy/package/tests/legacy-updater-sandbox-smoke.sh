#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $(id -u) -eq 0 ]] || {
  printf 'ERROR: legacy updater sandbox smoke must run as root\n' >&2
  exit 1
}
command -v systemd-run >/dev/null || {
  printf 'ERROR: systemd-run is required for the legacy updater sandbox smoke\n' >&2
  exit 1
}

root=$(mktemp -d)
unit="neproto-legacy-updater-smoke-$$"
trap 'rm -rf -- "$root"' EXIT
chmod 0755 "$root"

# Match an already-managed 0.5.x node. These directories acquired their
# setgid modes during the original unsandboxed installation, so the legacy
# updater only needs to preserve them during the transition.
mkdir -p "$root/etc/neproto/geodata" "$root/var/lib/neproto/update/inbox"
chown root:65532 "$root/etc/neproto/geodata" "$root/var/lib/neproto/update"
chmod 2770 "$root/etc/neproto/geodata"
chmod 2750 "$root/var/lib/neproto/update"

# Reproduce the mode inherited when the old updater extracts beneath
# /var/lib/neproto/update (2750). The fixed installer must remove it before
# recursively staging the standalone Next.js payload.
find "$package_dir/web" -type d -exec chmod 2755 {} +

systemd-run --quiet --wait --pipe --collect --service-type=exec \
  --unit "$unit" \
  --property=RestrictSUIDSGID=true \
  --property=NoNewPrivileges=true \
  /usr/bin/env NEPROTO_TEST_MODE=1 \
  "$package_dir/install.sh" --root "$root" --mode bare-metal \
  --domain vpn.example.com --addresses 8.8.8.8 \
  --web-domain admin.example.com --web-port 3100 \
  --non-interactive --skip-start

[[ -s $root/opt/neproto/web/.next/BUILD_ID ]]
[[ -d $root/opt/neproto/web/node_modules ]]
printf 'PASS: legacy RestrictSUIDSGID updater web staging\n'
