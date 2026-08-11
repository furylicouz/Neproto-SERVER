#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
baseline_package_dir=${1:-$package_dir}
[[ -x $baseline_package_dir/install.sh ]] || {
  printf 'ERROR: invalid legacy baseline package: %s\n' "$baseline_package_dir" >&2
  exit 2
}
[[ $(id -u) -eq 0 ]] || {
  printf 'ERROR: legacy updater sandbox smoke must run as root\n' >&2
  exit 1
}
command -v systemd-run >/dev/null || {
  printf 'ERROR: systemd-run is required for the legacy updater sandbox smoke\n' >&2
  exit 1
}

root=$(mktemp -d)
log=$(mktemp)
fakebin=$(mktemp -d)
unit="neproto-legacy-updater-smoke-$$"
trap 'rm -rf -- "$root" "$fakebin"; rm -f -- "$log"' EXIT
chmod 0755 "$root"

# Exercise the production GeoIP/GeoSite downloader without depending on the
# network. The fake curl writes deterministic payloads and valid checksums;
# update-geodata still performs its normal checksum validation, install and
# atomic rename inside the legacy RestrictSUIDSGID sandbox.
cat >"$fakebin/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

output=
url=
while (($#)); do
  case $1 in
    -o)
      output=${2-}
      shift 2
      ;;
    --proto|--retry|--connect-timeout|--max-time)
      shift 2
      ;;
    --tlsv1.2|-*)
      shift
      ;;
    *)
      url=$1
      shift
      ;;
  esac
done
[[ -n $output && -n $url ]]
if [[ $output == *.sha256sum ]]; then
  payload=${output%.sha256sum}
  [[ -f $payload ]]
  sha256sum "$payload" >"$output"
else
  printf 'verified fixture for %s\n' "${url##*/}" >"$output"
fi
EOF
chmod 0755 "$fakebin/curl"

# Create a complete managed-node baseline first. The restricted transaction
# below is therefore an update of existing server, web, credentials, routes,
# units and state rather than a disguised clean installation.
NEPROTO_TEST_MODE=1 "$baseline_package_dir/install.sh" --root "$root" --mode bare-metal \
  --domain vpn.example.com --addresses 8.8.8.8 \
  --web-domain admin.example.com --web-port 3100 \
  --non-interactive --skip-start >/dev/null
admin_secret_before=$(sha256sum "$root/etc/neproto/web-admin.secret" | awk '{print $1}')
https_path_before=$(jq -r '.https_path' "$root/etc/neproto/installation.json")
webrtc_path_before=$(jq -r '.webrtc_path' "$root/etc/neproto/installation.json")
http3_path_before=$(jq -r '.http3_path' "$root/etc/neproto/installation.json")

# Match the setgid state left by an already-managed 0.5.x node. The legacy
# updater must preserve these directories during its transition.
chown root:65532 "$root/etc/neproto/geodata" "$root/var/lib/neproto/update"
chmod 2770 "$root/etc/neproto/geodata"
chmod 2750 "$root/var/lib/neproto/update"

# Reproduce the mode inherited when the old updater extracts beneath
# /var/lib/neproto/update (2750). The fixed installer must remove it before
# recursively staging the standalone Next.js payload.
find "$package_dir/web" -type d -exec chmod 2755 {} +

if ! systemd-run --quiet --wait --pipe --collect --service-type=exec \
  --unit "$unit" \
  --property=RestrictSUIDSGID=true \
  --property=NoNewPrivileges=true \
  --property=PrivateTmp=true \
  --property=PrivateDevices=true \
  --property=ProtectHome=true \
  --property=ProtectKernelTunables=true \
  --property=ProtectKernelModules=true \
  --property=ProtectKernelLogs=true \
  --property=ProtectControlGroups=true \
  --property=LockPersonality=true \
  --property=RestrictRealtime=true \
  --property=RestrictNamespaces=true \
  --property=RemoveIPC=true \
  --property='RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6' \
  --property=SystemCallArchitectures=native \
  /usr/bin/env PATH="$fakebin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  NEPROTO_TEST_MODE=1 NEPROTO_GEODATA_TEST_DOWNLOAD=1 \
  "$package_dir/install.sh" --root "$root" --mode bare-metal \
  --domain vpn.example.com --addresses 8.8.8.8 \
  --web-domain admin.example.com --web-port 3100 \
  --non-interactive --skip-start 2>&1 | tee "$log"; then
  annotation=$(tail -n 20 "$log" | tr '\r\n' '  ' | sed 's/%/%25/g' | cut -c1-4000)
  printf '::error title=Legacy updater sandbox failed::%s\n' "$annotation"
  exit 1
fi

[[ -s $root/opt/neproto/web/.next/BUILD_ID ]]
[[ -d $root/opt/neproto/web/node_modules ]]
[[ $(sha256sum "$root/etc/neproto/web-admin.secret" | awk '{print $1}') == "$admin_secret_before" ]]
[[ $(jq -r '.https_path' "$root/etc/neproto/installation.json") == "$https_path_before" ]]
[[ $(jq -r '.webrtc_path' "$root/etc/neproto/installation.json") == "$webrtc_path_before" ]]
[[ $(jq -r '.http3_path' "$root/etc/neproto/installation.json") == "$http3_path_before" ]]
grep -qx 'verified fixture for geoip.dat' "$root/etc/neproto/geodata/geoip.dat"
grep -qx 'verified fixture for dlc.dat' "$root/etc/neproto/geodata/geosite.dat"
[[ $(stat -c %a "$root/etc/neproto/geodata") == 2770 ]]
[[ $(stat -c %a "$root/etc/neproto/geodata/geoip.dat") == 660 ]]
[[ $(stat -c %a "$root/etc/neproto/geodata/geosite.dat") == 660 ]]
[[ $(stat -c %a "$root/var/lib/neproto/update") == 2750 ]]
[[ -s $root/etc/neproto/server.json ]]
[[ -s $root/etc/caddy/Caddyfile ]]
[[ -s $root/etc/systemd/system/neproto-server.service ]]
[[ -s $root/etc/systemd/system/neproto-web.service ]]
[[ -s $root/etc/systemd/system/neproto-update.service ]]
! grep -q '^RestrictSUIDSGID=true$' "$root/etc/systemd/system/neproto-update.service"
expected_version=$(<"$package_dir/VERSION")
[[ $("$root/usr/local/bin/neproto-server" version) == "neproto-server $expected_version" ]]
[[ $("$root/usr/local/bin/neprotoctl" version) == "neprotoctl $expected_version" ]]
printf 'PASS: complete legacy RestrictSUIDSGID installer lifecycle\n'
