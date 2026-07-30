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
grep -Fq 'cp -R --no-preserve=ownership,mode,timestamps -- "$script_dir/web/." "$web_stage/"' \
  "$repository/deploy/package/install.sh"
! grep -Fq 'cp -a -- "$script_dir/web/."' "$repository/deploy/package/install.sh"

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

fixture=$standalone_dir/neproto-server-bundle-$version.tar.gz
fixture_root=$standalone_dir/neproto-server-bundle-$version
work_base=$standalone_dir/work
mkdir -p "$fixture_root" "$work_base"
cat >"$fixture_root/install.sh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'fixture installer executed\n'
printf 'fixture root: %s\n' "$(dirname -- "$0")"
EOF
chmod 0755 "$fixture_root/install.sh"
tar -czf "$fixture" -C "$standalone_dir" "neproto-server-bundle-$version"
(
  cd "$standalone_dir"
  sha256sum "$(basename "$fixture")" >"$(basename "$fixture").sha256"
)
output=$(NEPROTO_BOOTSTRAP_TEST_MODE=1 NEPROTO_TMPDIR="$work_base" "$repository/install.sh" --bootstrap-bundle "$fixture")
grep -q '^fixture installer executed$' <<<"$output"
grep -Fq "fixture root: $work_base/" <<<"$output"
[[ -s $fixture && -s $fixture.sha256 ]]
if find "$work_base" -mindepth 1 -print -quit | grep -q .; then
  printf 'standalone bootstrap leaked its temporary extraction directory\n' >&2
  exit 1
fi
printf 'PASS: root bootstrap contract\n'
