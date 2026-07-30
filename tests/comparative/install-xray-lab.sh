#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  printf 'usage: %s --source DIRECTORY [--root DIRECTORY] [--skip-start]\n' "$0" >&2
}

source_directory=
root=
skip_start=0
while (($#)); do
  case "$1" in
    --source)
      source_directory=${2:-}
      shift 2
      ;;
    --root)
      root=${2:-}
      shift 2
      ;;
    --skip-start)
      skip_start=1
      shift
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

[[ -n $source_directory ]] || { usage; exit 2; }
source_directory=$(cd -- "$source_directory" && pwd)
if [[ -n $root ]]; then
  root=$(mkdir -p -- "$root" && cd -- "$root" && pwd)
fi

for required in xray vision-server.json xhttp-server.json manifest.json; do
  [[ -f $source_directory/$required ]] || {
    printf 'missing lab artifact: %s\n' "$required" >&2
    exit 1
  }
done
[[ -x $source_directory/xray ]] || chmod 0700 "$source_directory/xray"
version=$($source_directory/xray version | awk 'NR == 1 { print $2 }')
[[ $version == 26.3.27 ]] || {
  printf 'unexpected Xray version: %s\n' "$version" >&2
  exit 1
}
for profile in vision xhttp; do
  "$source_directory/xray" run -test -c "$source_directory/$profile-server.json" >/dev/null
done

install_root=$root/opt/neproto-comparative
unit_root=$root/etc/systemd/system
install -d -m 0755 "$install_root/bin" "$install_root/config" "$unit_root"
install -m 0755 "$source_directory/xray" "$install_root/bin/xray"
install -m 0600 "$source_directory/vision-server.json" "$install_root/config/vision-server.json"
install -m 0600 "$source_directory/xhttp-server.json" "$install_root/config/xhttp-server.json"
install -m 0600 "$source_directory/manifest.json" "$install_root/manifest.json"

service_user=neproto-lab
if [[ -z $root ]]; then
  if ! getent group "$service_user" >/dev/null; then
    groupadd --system "$service_user"
  fi
  if ! id "$service_user" >/dev/null 2>&1; then
    useradd --system --gid "$service_user" --home-dir /nonexistent --shell /usr/sbin/nologin "$service_user"
  fi
  chown -R root:"$service_user" "$install_root/config"
  chmod 0640 "$install_root/config/"*.json
fi

write_unit() {
  local profile=$1 port=$2
  cat >"$unit_root/xray-lab-$profile.service" <<EOF
[Unit]
Description=Temporary NeProto comparative Xray $profile baseline
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$service_user
Group=$service_user
ExecStart=/opt/neproto-comparative/bin/xray run -c /opt/neproto-comparative/config/$profile-server.json
Restart=on-failure
RestartSec=2s
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_INET AF_INET6
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
# Expected isolated public listener: TCP $port
EOF
}

write_unit vision 18443
write_unit xhttp 18444

if [[ -z $root && $skip_start -eq 0 ]]; then
  systemctl daemon-reload
  systemctl enable --now xray-lab-vision.service xray-lab-xhttp.service
  systemctl is-active --quiet xray-lab-vision.service
  systemctl is-active --quiet xray-lab-xhttp.service
fi

printf 'Xray comparative lab installed; production TCP/UDP 443 was not modified.\n'
