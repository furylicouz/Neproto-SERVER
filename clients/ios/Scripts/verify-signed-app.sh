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
done

echo "Signed application and Packet Tunnel entitlements are valid."
