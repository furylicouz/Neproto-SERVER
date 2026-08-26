#!/usr/bin/env bash
set -Eeuo pipefail

package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
temporary_root=${NEPROTO_TEST_TMPDIR:-/var/lib}
[[ -d $temporary_root && -w $temporary_root ]] || {
  printf 'ERROR: test temporary root is not writable: %s\n' "$temporary_root" >&2
  exit 1
}

root=$(mktemp -d "$temporary_root/neproto-certificate-smoke.XXXXXX")
trap 'rm -rf -- "$root"' EXIT
domain=vpn.example.com
service_gid=65532
real_install=$(command -v install)

mkdir -p -- "$root/etc/neproto" "$root/etc/letsencrypt/live/$domain" "$root/test-bin"
cat >"$root/etc/neproto/installation.json" <<EOF
{
  "domain": "$domain",
  "mode": "docker",
  "service_gid": $service_gid
}
EOF
printf '%s\n' 'test full chain' >"$root/etc/letsencrypt/live/$domain/fullchain.pem"
printf '%s\n' 'test private key' >"$root/etc/letsencrypt/live/$domain/privkey.pem"
cat >"$root/test-bin/install" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
arguments=("$@")
for ((index = 0; index < ${#arguments[@]}; index++)); do
  case ${arguments[index]} in
    -g|--group)
      ((index + 1 < ${#arguments[@]})) || exit 96
      [[ ${arguments[index + 1]} =~ ^[0-9]+$ ]] && {
        printf 'install received a numeric group: %s\n' "${arguments[index + 1]}" >&2
        exit 95
      }
      ;;
    --group=[0-9]*)
      printf 'install received a numeric group: %s\n' "${arguments[index]#*=}" >&2
      exit 95
      ;;
  esac
done
exec "${NEPROTO_REAL_INSTALL:?}" "$@"
EOF
chmod 0755 "$root/test-bin/install"

NEPROTO_REAL_INSTALL="$real_install" PATH="$root/test-bin:$PATH" \
  NEPROTO_TEST_MODE=1 NEPROTO_TEST_ROOT="$root" \
  "$package_dir/scripts/sync-certificate.sh"

cmp -s "$root/etc/letsencrypt/live/$domain/fullchain.pem" "$root/etc/neproto/tls/fullchain.pem"
cmp -s "$root/etc/letsencrypt/live/$domain/privkey.pem" "$root/etc/neproto/tls/privkey.pem"
[[ $(stat -c %g "$root/etc/neproto/tls") == "$service_gid" ]]
[[ $(stat -c %g "$root/etc/neproto/tls/fullchain.pem") == "$service_gid" ]]
[[ $(stat -c %g "$root/etc/neproto/tls/privkey.pem") == "$service_gid" ]]
[[ $(stat -c %a "$root/etc/neproto/tls/fullchain.pem") == 644 ]]
[[ $(stat -c %a "$root/etc/neproto/tls/privkey.pem") == 640 ]]

printf 'PASS: numeric Docker certificate ownership\n'
