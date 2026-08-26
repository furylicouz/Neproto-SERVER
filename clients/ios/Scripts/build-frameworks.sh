#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
ROOT_DIR=$(cd "$SCRIPT_DIR/../../.." && pwd)
readonly ROOT_DIR
readonly IOS_DIR="$ROOT_DIR/clients/ios"
readonly CACHE_DIR="${NEPROTO_IOS_CACHE:-$HOME/Library/Caches/NeProtoBuild}"
readonly FRAMEWORK_DIR="$IOS_DIR/Frameworks"
readonly GO_VERSION="go1.26.7"
readonly GOMOBILE_VERSION="v0.0.0-20260709172247-6129f5bee9d5"
NP2_VERSION=$(tr -d '[:space:]' <"$ROOT_DIR/VERSION")
readonly NP2_VERSION
if [[ ! "$NP2_VERSION" =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Invalid NP/2 release version: $NP2_VERSION" >&2
    exit 1
fi

GO_BIN="${GO_BIN:-$(command -v go || true)}"
if [[ -z "$GO_BIN" ]]; then
    echo "Go $GO_VERSION is required. Set GO_BIN to its executable." >&2
    exit 1
fi
if [[ "$($GO_BIN version)" != "go version $GO_VERSION "* ]]; then
    echo "Expected $GO_VERSION, got: $($GO_BIN version)" >&2
    exit 1
fi

mkdir -p "$CACHE_DIR/bin" "$FRAMEWORK_DIR"
GO_DIR=$(dirname "$GO_BIN")
PATH="$GO_DIR:$CACHE_DIR/bin:$PATH"
export PATH
export GOBIN="$CACHE_DIR/bin"

"$GO_BIN" install "golang.org/x/mobile/cmd/gomobile@$GOMOBILE_VERSION"
gomobile init

(
    cd "$ROOT_DIR"
    gomobile bind \
        -target=ios \
        -ldflags="-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$NP2_VERSION" \
        -o "$FRAMEWORK_DIR/NP2Mobile.xcframework" \
        ./mobile/np2mobile
)

echo "NP2Mobile framework is ready in $FRAMEWORK_DIR"
