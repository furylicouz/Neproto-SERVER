#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  printf 'run as root\n' >&2
  exit 1
fi

systemctl disable --now xray-lab-vision.service xray-lab-xhttp.service 2>/dev/null || true
rm -f -- /etc/systemd/system/xray-lab-vision.service /etc/systemd/system/xray-lab-xhttp.service
systemctl daemon-reload
rm -rf -- /opt/neproto-comparative
printf 'Xray comparative lab removed; firewall and production services were not changed.\n'
