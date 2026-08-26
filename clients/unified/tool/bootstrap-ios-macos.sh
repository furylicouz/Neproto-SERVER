#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
readonly VERSIONS_FILE="$SCRIPT_DIR/versions.json"
readonly TOOLCHAIN_CACHE="${NEPROTO_TOOLCHAIN_CACHE:-$HOME/Library/Caches/NeProtoUnifiedToolchains}"

fail() {
    echo "iOS toolchain bootstrap error: $*" >&2
    exit 1
}

[[ $(uname -s) == Darwin ]] || fail 'macOS is required'
[[ $(uname -m) == arm64 ]] || fail 'Apple Silicon (arm64) is required'
command -v jq >/dev/null 2>&1 || fail 'jq is required'
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v shasum >/dev/null 2>&1 || fail 'shasum is required'
command -v ditto >/dev/null 2>&1 || fail 'ditto is required'

json_string() {
    jq -er --arg key "$1" '.[$key] | select(type == "string" and length > 0)' "$VERSIONS_FILE"
}

download_verified() {
    local url=$1
    local sha256=$2
    local destination=$3
    local partial="${destination}.partial"

    if [[ -f "$destination" ]] && \
        printf '%s  %s\n' "$sha256" "$destination" | shasum -a 256 --check --status; then
        return
    fi

    curl --fail --location --retry 3 --output "$partial" "$url"
    if ! printf '%s  %s\n' "$sha256" "$partial" | shasum -a 256 --check --status; then
        rm -f -- "$partial"
        fail "SHA-256 verification failed for $url"
    fi
    mv -f -- "$partial" "$destination"
}

FLUTTER_VERSION=$(json_string flutter)
readonly FLUTTER_VERSION
FLUTTER_URL=$(json_string flutter_macos_archive)
readonly FLUTTER_URL
FLUTTER_SHA256=$(json_string flutter_macos_sha256)
readonly FLUTTER_SHA256
GO_VERSION=$(json_string go_macos_version)
readonly GO_VERSION
GO_URL=$(json_string go_macos_archive)
readonly GO_URL
GO_SHA256=$(json_string go_macos_sha256)
readonly GO_SHA256

readonly DOWNLOAD_DIR="$TOOLCHAIN_CACHE/downloads"
readonly STAGING_DIR="$TOOLCHAIN_CACHE/staging"
readonly FLUTTER_ROOT="$TOOLCHAIN_CACHE/flutter/$FLUTTER_VERSION"
readonly GO_ROOT="$TOOLCHAIN_CACHE/go/go$GO_VERSION"
readonly FLUTTER_ARCHIVE="$DOWNLOAD_DIR/flutter-$FLUTTER_VERSION-macos-arm64.zip"
readonly GO_ARCHIVE="$DOWNLOAD_DIR/go$GO_VERSION-darwin-arm64.tar.gz"

mkdir -p "$DOWNLOAD_DIR" "$STAGING_DIR" "$(dirname "$FLUTTER_ROOT")" "$(dirname "$GO_ROOT")"

download_verified "$FLUTTER_URL" "$FLUTTER_SHA256" "$FLUTTER_ARCHIVE"
download_verified "$GO_URL" "$GO_SHA256" "$GO_ARCHIVE"

work_dir=$(mktemp -d "$STAGING_DIR/bootstrap.XXXXXX")
cleanup() {
    case "$work_dir" in
        "$STAGING_DIR"/bootstrap.*) rm -rf -- "$work_dir" ;;
        *) echo "Refusing to remove unexpected staging path: $work_dir" >&2 ;;
    esac
}
trap cleanup EXIT

if [[ ! -x "$FLUTTER_ROOT/bin/flutter" ]]; then
    [[ ! -e "$FLUTTER_ROOT" ]] || fail "invalid existing Flutter directory: $FLUTTER_ROOT"
    mkdir -p "$work_dir/flutter-extract"
    ditto -x -k "$FLUTTER_ARCHIVE" "$work_dir/flutter-extract"
    [[ -x "$work_dir/flutter-extract/flutter/bin/flutter" ]] || fail 'Flutter archive layout is invalid'
    mv -- "$work_dir/flutter-extract/flutter" "$FLUTTER_ROOT"
fi

if [[ ! -x "$GO_ROOT/bin/go" ]]; then
    [[ ! -e "$GO_ROOT" ]] || fail "invalid existing Go directory: $GO_ROOT"
    mkdir -p "$work_dir/go-extract"
    tar -xzf "$GO_ARCHIVE" -C "$work_dir/go-extract"
    [[ -x "$work_dir/go-extract/go/bin/go" ]] || fail 'Go archive layout is invalid'
    mv -- "$work_dir/go-extract/go" "$GO_ROOT"
fi

actual_flutter=$($FLUTTER_ROOT/bin/flutter --version --machine | jq -er '.frameworkVersion')
[[ "$actual_flutter" == "$FLUTTER_VERSION" ]] || \
    fail "expected Flutter $FLUTTER_VERSION, got $actual_flutter"

actual_go=$($GO_ROOT/bin/go version)
[[ "$actual_go" == "go version go$GO_VERSION darwin/arm64" ]] || \
    fail "expected Go $GO_VERSION for darwin/arm64, got $actual_go"

printf 'FLUTTER_ROOT=%s\n' "$FLUTTER_ROOT"
printf 'GO_BIN=%s\n' "$GO_ROOT/bin/go"
printf 'PASS: pinned iOS toolchains are ready.\n'
