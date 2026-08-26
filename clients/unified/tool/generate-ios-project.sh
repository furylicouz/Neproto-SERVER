#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
IOS_DIR=$(cd "$SCRIPT_DIR/../app/ios" && pwd)
readonly IOS_DIR

readonly XCODEGEN_VERSION="2.45.4"
readonly XCODEGEN_COMMIT="8d3d3476a69ae3e5d68e1adccc701c410c05eb36"
readonly TOOLCHAIN_CACHE="${NEPROTO_TOOLCHAIN_CACHE:-$HOME/Library/Caches/NeProtoUnifiedToolchains}"
readonly CACHED_XCODEGEN="$TOOLCHAIN_CACHE/xcodegen/$XCODEGEN_VERSION/xcodegen"

XCODEGEN_BIN="${XCODEGEN_BIN:-$CACHED_XCODEGEN}"
if [[ ! -x "$XCODEGEN_BIN" ]]; then
    command -v git >/dev/null 2>&1 || { echo 'git is required to bootstrap XcodeGen.' >&2; exit 1; }
    command -v swift >/dev/null 2>&1 || { echo 'Swift is required to bootstrap XcodeGen.' >&2; exit 1; }

    mkdir -p "$TOOLCHAIN_CACHE/xcodegen/$XCODEGEN_VERSION"
    build_dir=$(mktemp -d "${TMPDIR:-/tmp}/neproto-xcodegen.XXXXXX")
    cleanup() {
        rm -rf "$build_dir"
    }
    trap cleanup EXIT

    git clone --quiet --depth 1 --branch "$XCODEGEN_VERSION" \
        https://github.com/yonaskolb/XcodeGen.git "$build_dir/source"
    actual_commit=$(git -C "$build_dir/source" rev-parse HEAD)
    if [[ "$actual_commit" != "$XCODEGEN_COMMIT" ]]; then
        echo "Expected XcodeGen commit $XCODEGEN_COMMIT, got $actual_commit" >&2
        exit 1
    fi
    swift build --package-path "$build_dir/source" -c release
    install -m 0755 "$build_dir/source/.build/release/xcodegen" "$CACHED_XCODEGEN"
    XCODEGEN_BIN="$CACHED_XCODEGEN"
fi

if [[ "$($XCODEGEN_BIN --version)" != "Version: $XCODEGEN_VERSION" ]]; then
    echo "Expected XcodeGen $XCODEGEN_VERSION, got: $($XCODEGEN_BIN --version)" >&2
    exit 1
fi
if [[ ! -d "$IOS_DIR/Frameworks/NP2Mobile.xcframework" ]]; then
    echo "Run clients/unified/tool/build-ios-framework.sh first." >&2
    exit 1
fi
if [[ ! -d "$IOS_DIR/Flutter/ephemeral/Packages/FlutterGeneratedPluginSwiftPackage" ]]; then
    echo "Run flutter pub get in clients/unified/app before generating the iOS project." >&2
    exit 1
fi

(
    cd "$IOS_DIR"
    "$XCODEGEN_BIN" generate --spec project.yml
)

echo "Generated $IOS_DIR/Runner.xcodeproj with a separate PacketTunnel target."
