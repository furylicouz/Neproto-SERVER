#!/usr/bin/env bash
set -Eeuo pipefail

version=${1:?usage: deploy/build-server-bundle.sh VERSION [OUTPUT_DIRECTORY]}
[[ $version =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'invalid release version: %s\n' "$version" >&2
  exit 2
}

repository=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
release_version=$(tr -d '[:space:]' <"$repository/VERSION")
[[ $version == "$release_version" ]] || {
  printf 'release version %s does not match VERSION (%s)\n' "$version" "$release_version" >&2
  exit 2
}
package=$repository/deploy/package
output_directory=${2:-$repository/.tools}
name=neproto-server-bundle-$version
archive=$output_directory/$name.tar.gz
go_bin=${GO_BIN:-go}
npm_bin=${NPM_BIN:-npm}
node_version=22.23.2
caddy_version=v2.11.4
source_date_epoch=${SOURCE_DATE_EPOCH:-0}
[[ $source_date_epoch =~ ^[0-9]+$ ]] || {
  printf 'invalid SOURCE_DATE_EPOCH: %s\n' "$source_date_epoch" >&2
  exit 2
}
staging=$(mktemp -d)
trap 'rm -rf -- "$staging"' EXIT

install -d -m 0755 "$output_directory" "$staging/$name"
cp -a -- "$package/." "$staging/$name/"

# Resolve the exact Caddy module once in an isolated build module. `go install`
# with GOBIN cannot cross-compile, while `go build -o` can produce both target
# architectures without writing generated module files into this repository.
caddy_source=$staging/caddy-source
install -d -m 0755 "$caddy_source"
(
  cd -- "$caddy_source"
  "$go_bin" mod init neproto.local/release-caddy
  "$go_bin" get "github.com/caddyserver/caddy/v2/cmd/caddy@$caddy_version"
)

# Always build the release executables from this checkout.  Copying binaries
# from deploy/package made it possible to create a correctly named archive
# containing a stale or development server.
for architecture in amd64 arm64; do
  release_bin=$staging/$name/bin/$architecture
  install -d -m 0755 "$release_bin"
  (
    cd -- "$repository"
    CGO_ENABLED=0 GOOS=linux GOARCH=$architecture "$go_bin" build \
      -trimpath \
      -ldflags "-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$version" \
      -o "$release_bin/neproto-server" ./cmd/neproto-server
    CGO_ENABLED=0 GOOS=linux GOARCH=$architecture "$go_bin" build \
      -trimpath \
      -ldflags "-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$version" \
      -o "$release_bin/neprotoctl" ./cmd/neprotoctl
    CGO_ENABLED=0 GOOS=linux GOARCH=$architecture "$go_bin" build \
      -trimpath \
      -ldflags "-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$version" \
      -o "$release_bin/neproto-updater" ./cmd/neproto-updater
    (
      cd -- "$caddy_source"
      CGO_ENABLED=0 GOOS=linux GOARCH=$architecture "$go_bin" build \
        -trimpath -ldflags "-s -w" -o "$release_bin/caddy" \
        github.com/caddyserver/caddy/v2/cmd/caddy
    )
  )

  case "$architecture" in
    amd64)
      node_platform=x64
      node_sha256=d60acfe00a2932254bb0ad20e01b0d74397a0875595de719654b214f4b03f307
      ;;
    arm64)
      node_platform=arm64
      node_sha256=fff4078c5def658577f92c88db7db3bc0072924bfb93fe52c1e744a54e94abb8
      ;;
  esac
  if [[ -n ${NODE_PREBUILT_DIR:-} ]]; then
    prebuilt_node=$NODE_PREBUILT_DIR/$architecture/node
    [[ -s $prebuilt_node ]] || {
      printf 'missing explicit prebuilt Node runtime: %s\n' "$prebuilt_node" >&2
      exit 2
    }
    printf 'using explicit offline Node runtime for %s\n' "$architecture"
    install -m 0755 "$prebuilt_node" "$release_bin/node"
  else
    node_archive=node-v$node_version-linux-$node_platform.tar.xz
    node_cache=${NODE_DIST_DIR:-$staging/node-downloads}
    install -d -m 0755 "$node_cache"
    if [[ ! -s $node_cache/$node_archive ]]; then
      curl --fail --location --proto '=https' --tlsv1.2 \
        "https://nodejs.org/download/release/v$node_version/$node_archive" \
        --output "$node_cache/$node_archive"
    fi
    printf '%s  %s\n' "$node_sha256" "$node_cache/$node_archive" | sha256sum --check --status || {
      printf 'Node.js checksum verification failed for %s\n' "$architecture" >&2
      exit 2
    }
    node_extract=$staging/node-$architecture
    install -d -m 0755 "$node_extract"
    tar -xJf "$node_cache/$node_archive" -C "$node_extract" \
      "node-v$node_version-linux-$node_platform/bin/node"
    install -m 0755 "$node_extract/node-v$node_version-linux-$node_platform/bin/node" "$release_bin/node"
  fi
done
printf '%s\n' "$version" >"$staging/$name/VERSION"

# Build NeProto Web once and package Next.js' traced standalone runtime. The
# target VPS never needs the source tree or development dependencies.
web_source=$repository/neproto-web
[[ -f $web_source/package-lock.json ]] || {
  printf 'missing NeProto Web lockfile: %s\n' "$web_source/package-lock.json" >&2
  exit 2
}
(
  cd -- "$web_source"
  "$npm_bin" ci --ignore-scripts
  NEPROTO_VERSION="$version" "$npm_bin" run build
)
web_release=$staging/$name/web
install -d -m 0755 "$web_release/.next/static"
cp -a -- "$web_source/.next/standalone/." "$web_release/"
cp -a -- "$web_source/.next/static/." "$web_release/.next/static/"
if [[ -d $web_source/public ]]; then
  cp -a -- "$web_source/public" "$web_release/public"
fi
[[ -s $web_release/server.js && -s $web_release/.next/BUILD_ID ]] || {
  printf 'incomplete NeProto Web standalone output\n' >&2
  exit 2
}

# Windows worktrees do not carry Unix mode reliably. Normalize the complete
# release tree explicitly so extraction never creates world-writable content.
find "$staging/$name" -type d -exec chmod 0755 {} +
find "$staging/$name" -type f -exec chmod 0644 {} +
chmod 0755 "$staging/$name/install.sh"
chmod 0755 "$staging/$name/cluster-node-install.sh"
find "$staging/$name/tests" -maxdepth 1 -type f -name '*.sh' -exec chmod 0755 {} +
find "$staging/$name/scripts" -maxdepth 1 -type f -name '*.sh' -exec chmod 0755 {} +
find "$staging/$name/bin" -mindepth 2 -maxdepth 2 -type f -exec chmod 0755 {} +
rm -f -- "$staging/$name/docker/caddy" "$staging/$name/docker/neproto-server"

"$staging/$name/tests/web-bundle-smoke.sh"

tar -C "$staging" --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 \
  --numeric-owner --format=gnu -cf - "$name" | gzip -n >"$archive"
(cd -- "$output_directory" && sha256sum "$(basename -- "$archive")" >"$(basename -- "$archive").sha256")
printf 'created %s\n' "$archive"
cat "$archive.sha256"
