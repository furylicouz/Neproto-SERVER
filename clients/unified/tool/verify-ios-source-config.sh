#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
ROOT_DIR=$(cd "$SCRIPT_DIR/../../.." && pwd)
readonly ROOT_DIR

fail() {
    echo "iOS source configuration error: $*" >&2
    exit 1
}

release_version=$(tr -d '[:space:]' <"$ROOT_DIR/VERSION")
[[ "$release_version" =~ ^np2-([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || \
    fail "invalid repository VERSION: $release_version"
readonly semantic_version="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
readonly build_number="${BASH_REMATCH[3]}"

pubspec_version=$(sed -n 's/^version: \([^+][^+]*\)+\([0-9][0-9]*\)$/\1/p' \
    "$ROOT_DIR/clients/unified/app/pubspec.yaml")
pubspec_build=$(sed -n 's/^version: [^+][^+]*+\([0-9][0-9]*\)$/\1/p' \
    "$ROOT_DIR/clients/unified/app/pubspec.yaml")
[[ "$pubspec_version" == "$semantic_version" ]] || \
    fail "pubspec version $pubspec_version does not match $semantic_version"
[[ "$pubspec_build" == "$build_number" ]] || \
    fail "pubspec build $pubspec_build does not match $build_number"

project_version=$(sed -n 's/^[[:space:]]*MARKETING_VERSION: "\([^"]*\)"$/\1/p' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml")
project_build=$(sed -n 's/^[[:space:]]*CURRENT_PROJECT_VERSION: "\([0-9][0-9]*\)"$/\1/p' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml")
[[ "$project_version" == "$semantic_version" ]] || \
    fail "Xcode marketing version $project_version does not match $semantic_version"
[[ "$project_build" == "$build_number" ]] || \
    fail "Xcode build $project_build does not match $build_number"

module_toolchain=$(sed -n 's/^toolchain \(go[0-9][0-9.]*\)$/\1/p' "$ROOT_DIR/go.mod")
framework_toolchain=$(sed -n 's/^readonly GO_VERSION="\([^"]*\)"$/\1/p' \
    "$ROOT_DIR/clients/unified/tool/build-ios-framework.sh")
[[ -n "$module_toolchain" ]] || fail 'go.mod has no pinned toolchain'
[[ "$framework_toolchain" == "$module_toolchain" ]] || \
    fail "framework Go $framework_toolchain does not match $module_toolchain"

grep -q 'PRODUCT_BUNDLE_IDENTIFIER: com.neproto.ios$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner bundle identifier is not pinned'
grep -q 'PRODUCT_BUNDLE_IDENTIFIER: com.neproto.ios.PacketTunnel$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'PacketTunnel bundle identifier is not pinned'

echo "PASS: iOS source configuration matches $release_version and $module_toolchain"
