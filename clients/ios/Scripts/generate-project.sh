#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
IOS_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
readonly IOS_DIR
readonly CACHE_DIR="${NEPROTO_IOS_CACHE:-$HOME/Library/Caches/NeProtoBuild}"
readonly XCODEGEN_VERSION="2.45.4"
readonly XCODEGEN_COMMIT="8d3d3476a69ae3e5d68e1adccc701c410c05eb36"
readonly XCODEGEN_DIR="$CACHE_DIR/XcodeGen-$XCODEGEN_VERSION"

XCODEGEN_BIN="${XCODEGEN_BIN:-$(command -v xcodegen || true)}"
if [[ -z "$XCODEGEN_BIN" ]]; then
    if [[ ! -d "$XCODEGEN_DIR/.git" ]]; then
        mkdir -p "$CACHE_DIR"
        git clone --branch "$XCODEGEN_VERSION" --depth 1 \
            https://github.com/yonaskolb/XcodeGen.git "$XCODEGEN_DIR"
    fi
    if [[ "$(git -C "$XCODEGEN_DIR" rev-parse HEAD)" != "$XCODEGEN_COMMIT" ]]; then
        echo "Unexpected XcodeGen commit in $XCODEGEN_DIR." >&2
        exit 1
    fi
    (
        cd "$XCODEGEN_DIR"
        swift build -c release
    )
    XCODEGEN_BIN="$XCODEGEN_DIR/.build/release/xcodegen"
fi

if [[ ! -d "$IOS_DIR/Frameworks/NP2Mobile.xcframework" ]]; then
    echo "Missing native frameworks. Run Scripts/build-frameworks.sh first." >&2
    exit 1
fi

(
    cd "$IOS_DIR"
    "$XCODEGEN_BIN" generate --spec project.yml
)

echo "Open $IOS_DIR/NeProto.xcodeproj"
