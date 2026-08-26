#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
IOS_DIR=$(cd "$SCRIPT_DIR/../app/ios" && pwd)
readonly IOS_DIR

XCODEGEN_BIN="${XCODEGEN_BIN:-$(command -v xcodegen || true)}"
if [[ -z "$XCODEGEN_BIN" ]]; then
    echo "XcodeGen 2.45.1 is required. Set XCODEGEN_BIN explicitly." >&2
    exit 1
fi
if [[ "$($XCODEGEN_BIN --version)" != "Version: 2.45.1" ]]; then
    echo "Expected XcodeGen 2.45.1, got: $($XCODEGEN_BIN --version)" >&2
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
