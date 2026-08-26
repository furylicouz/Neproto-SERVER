#!/bin/bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 /path/to/NeProto.app" >&2
    exit 2
fi

readonly APP_PATH="$1"
readonly EXTENSION_PATH="$APP_PATH/PlugIns/PacketTunnel.appex"
readonly NETWORK_EXTENSION_KEY="com.apple.developer.networking.networkextension"
readonly KEYCHAIN_GROUP_KEY="keychain-access-groups"
shared_runtime_group=""

for bundle in "$APP_PATH" "$EXTENSION_PATH"; do
    if [[ ! -d "$bundle" ]]; then
        echo "Missing signed bundle: $bundle" >&2
        exit 1
    fi

    entitlements="$(codesign -d --entitlements :- "$bundle" 2>&1)"
    if ! grep -q "$NETWORK_EXTENSION_KEY" <<<"$entitlements"; then
        echo "Missing Network Extension entitlement in $bundle" >&2
        exit 1
    fi
    if ! grep -q "packet-tunnel-provider" <<<"$entitlements"; then
        echo "Missing packet-tunnel-provider entitlement in $bundle" >&2
        exit 1
    fi
    if ! grep -q "$KEYCHAIN_GROUP_KEY" <<<"$entitlements"; then
        echo "Missing shared Keychain entitlement in $bundle" >&2
        exit 1
    fi

    runtime_group="$(/usr/libexec/PlistBuddy -c "Print :NeProtoKeychainAccessGroup" "$bundle/Info.plist" 2>/dev/null || true)"
    if [[ -z "$runtime_group" || "$runtime_group" == *'$('* ]]; then
        echo "Missing or unresolved NeProtoKeychainAccessGroup in $bundle" >&2
        exit 1
    fi
    if ! grep -Fq "<string>$runtime_group</string>" <<<"$entitlements"; then
        echo "Runtime Keychain group is not present in signed entitlements for $bundle" >&2
        exit 1
    fi
    if [[ -n "$shared_runtime_group" && "$runtime_group" != "$shared_runtime_group" ]]; then
        echo "Application and Packet Tunnel use different runtime Keychain groups" >&2
        exit 1
    fi
    shared_runtime_group="$runtime_group"
done

echo "Signed application and Packet Tunnel entitlements are valid."
