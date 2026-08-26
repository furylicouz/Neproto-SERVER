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

json_string() {
    local key=$1
    sed -n "s/^[[:space:]]*\"$key\": \"\([^\"]*\)\"[,]\{0,1\}$/\1/p" \
        "$ROOT_DIR/clients/unified/tool/versions.json"
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

pinned_go=$(json_string go_macos_version)
[[ "go$pinned_go" == "$module_toolchain" ]] || \
    fail "macOS Go pin $pinned_go does not match $module_toolchain"
[[ $(json_string go_macos_archive) == "https://go.dev/dl/$module_toolchain.darwin-arm64.tar.gz" ]] || \
    fail 'macOS Go archive does not match the pinned toolchain'
[[ $(json_string go_macos_sha256) =~ ^[a-f0-9]{64}$ ]] || \
    fail 'macOS Go archive SHA-256 is not pinned'

flutter_version=$(json_string flutter)
[[ $(json_string flutter_macos_archive) == *"flutter_macos_arm64_${flutter_version}-stable.zip" ]] || \
    fail 'macOS Flutter archive does not match the pinned Flutter version'
[[ $(json_string flutter_macos_sha256) =~ ^[a-f0-9]{64}$ ]] || \
    fail 'macOS Flutter archive SHA-256 is not pinned'

xcodegen_version=$(json_string xcodegen)
xcodegen_commit=$(json_string xcodegen_commit)
[[ "$xcodegen_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'XcodeGen version is not pinned'
[[ "$xcodegen_commit" =~ ^[a-f0-9]{40}$ ]] || fail 'XcodeGen commit is not pinned'
grep -q "readonly XCODEGEN_VERSION=\"$xcodegen_version\"" \
    "$ROOT_DIR/clients/unified/tool/generate-ios-project.sh" || \
    fail 'XcodeGen script version does not match versions.json'
grep -q "readonly XCODEGEN_COMMIT=\"$xcodegen_commit\"" \
    "$ROOT_DIR/clients/unified/tool/generate-ios-project.sh" || \
    fail 'XcodeGen script commit does not match versions.json'

grep -q 'PRODUCT_BUNDLE_IDENTIFIER: com.neproto.ios$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner bundle identifier is not pinned'
grep -q 'PRODUCT_BUNDLE_IDENTIFIER: com.neproto.ios.PacketTunnel$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'PacketTunnel bundle identifier is not pinned'
grep -q '^[[:space:]]*executable: Runner$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner scheme has no explicit app executable'
grep -q '^[[:space:]]*macroExpansion: Runner$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner test action has no explicit macro expansion target'
grep -q '^[[:space:]]*SWIFT_VERSION: "5.0"$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Flutter iOS targets must use Swift 5 compatibility mode'
grep -q '^[[:space:]]*CLANG_ENABLE_MODULES: YES$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Flutter iOS targets must enable Clang modules'
for plist in \
    "$ROOT_DIR/clients/unified/app/ios/Runner/Info.plist" \
    "$ROOT_DIR/clients/unified/app/ios/PacketTunnel/Info.plist"; do
    grep -Fq '<string>$(DEVELOPMENT_TEAM).ru.neproto.shared</string>' "$plist" || \
        fail "runtime Keychain group must include DEVELOPMENT_TEAM in $plist"
done
for entitlements in \
    "$ROOT_DIR/clients/unified/app/ios/Runner/Runner.entitlements" \
    "$ROOT_DIR/clients/unified/app/ios/PacketTunnel/PacketTunnel.entitlements"; do
    grep -Fq '<string>$(AppIdentifierPrefix)ru.neproto.shared</string>' "$entitlements" || \
        fail "signed Keychain group must include AppIdentifierPrefix in $entitlements"
done
grep -q '^[[:space:]]*- name: Run Prepare Flutter Framework Script$' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner scheme has no Flutter SPM prepare action'
grep -Fq 'script: /bin/sh "$FLUTTER_ROOT/packages/flutter_tools/bin/xcode_backend.sh" prepare' \
    "$ROOT_DIR/clients/unified/app/ios/project.yml" || fail 'Runner scheme prepare action is invalid'

readonly generated_project="$ROOT_DIR/clients/unified/app/ios/Runner.xcodeproj/project.pbxproj"
readonly generated_scheme="$ROOT_DIR/clients/unified/app/ios/Runner.xcodeproj/xcshareddata/xcschemes/Runner.xcscheme"
grep -q 'PBXNativeTarget "PacketTunnel"' "$generated_project" || \
    fail 'generated Xcode project has no PacketTunnel target'
grep -q 'PRODUCT_BUNDLE_IDENTIFIER = com.neproto.ios.PacketTunnel;' "$generated_project" || \
    fail 'generated Xcode project has no PacketTunnel bundle identifier'
grep -q 'BlueprintName = "Runner"' "$generated_scheme" || \
    fail 'generated Runner scheme has no Runner buildable'
grep -q 'xcode_backend.sh.*prepare' "$generated_scheme" || \
    fail 'generated Runner scheme has no Flutter SPM prepare action'

echo "PASS: iOS source configuration matches $release_version and $module_toolchain"
