#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
ROOT_DIR=$(cd "$SCRIPT_DIR/../../.." && pwd)
readonly ROOT_DIR
readonly IOS_DIR="$ROOT_DIR/clients/unified/app/ios"
readonly CACHE_DIR="${NEPROTO_IOS_CACHE:-$HOME/Library/Caches/NeProtoUnifiedBuild}"
readonly GO_VERSION="go1.26.5"
readonly GOMOBILE_VERSION="v0.0.0-20260709172247-6129f5bee9d5"
NP2_VERSION=$(tr -d '[:space:]' <"$ROOT_DIR/VERSION")
readonly NP2_VERSION

if [[ ! "$NP2_VERSION" =~ ^np2-[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Invalid NP/2 version: $NP2_VERSION" >&2
    exit 1
fi

GO_BIN="${GO_BIN:-$(command -v go || true)}"
if [[ -z "$GO_BIN" ]] || [[ "$($GO_BIN version)" != "go version $GO_VERSION "* ]]; then
    echo "Go $GO_VERSION is required. Set GO_BIN to the exact executable." >&2
    exit 1
fi

mkdir -p "$CACHE_DIR/bin" "$CACHE_DIR/output" "$IOS_DIR/Frameworks"
export GOBIN="$CACHE_DIR/bin"
export PATH="$(dirname "$GO_BIN"):$CACHE_DIR/bin:$PATH"

"$GO_BIN" install "golang.org/x/mobile/cmd/gomobile@$GOMOBILE_VERSION"
"$CACHE_DIR/bin/gomobile" init

STAGE_DIR=$(mktemp -d "$CACHE_DIR/output/np2mobile.XXXXXX")
readonly STAGE_DIR
readonly STAGED_FRAMEWORK="$STAGE_DIR/NP2Mobile.xcframework"

(
    cd "$ROOT_DIR"
    "$CACHE_DIR/bin/gomobile" bind \
        -target=ios \
        -ldflags="-s -w -X neproto.local/chameleon/internal/buildinfo.Version=$NP2_VERSION" \
        -o "$STAGED_FRAMEWORK" \
        ./mobile/np2mobile
)

if [[ -e "$IOS_DIR/Frameworks/NP2Mobile.xcframework" ]]; then
    readonly PREVIOUS_FRAMEWORK="$CACHE_DIR/output/NP2Mobile.previous.$(date -u +%Y%m%dT%H%M%SZ).xcframework"
    mv "$IOS_DIR/Frameworks/NP2Mobile.xcframework" "$PREVIOUS_FRAMEWORK"
fi
mv "$STAGED_FRAMEWORK" "$IOS_DIR/Frameworks/NP2Mobile.xcframework"
rmdir "$STAGE_DIR"
echo "NP2Mobile.xcframework built for the strict HTTP/3 iOS candidate."
