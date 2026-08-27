# Spec: Native SwiftUI Home for iOS

## Objective

Replace only the iOS application UI with a new native SwiftUI home screen while
preserving the existing NP/2 data plane, `PacketTunnelProvider`, profile import,
Keychain storage, cluster synchronization, and Windows client.

The first screen is deliberately system-native:

- a native navigation header with `NeProto` on the leading side;
- a trailing QR scanner button;
- an inset grouped connection section with an icon, status text, and native VPN
  `Toggle`;
- one inset grouped subscription section per imported subscription;
- server rows inside the subscription, each with a location emoji, server name,
  address, availability state, and native selected checkmark.

## Tech Stack

- SwiftUI and `NavigationStack`
- NetworkExtension through the existing `VPNService`
- Existing `NeProtoCore` Swift package
- Existing `NP2Mobile.xcframework` Go data plane
- Existing `QRScannerView` and `ProfileStore`

No new dependency is permitted for this screen.

## Commands

Run on the source-of-truth Mac checkout:

```bash
cd /Users/intimnyjprysik/Downloads/Neproto/clients/ios/Core
swift test

cd /Users/intimnyjprysik/Downloads/Neproto
clients/ios/Scripts/generate-project.sh
xcodebuild -project clients/ios/NeProto.xcodeproj -scheme NeProto \
  -configuration Debug -sdk iphonesimulator CODE_SIGNING_ALLOWED=NO build
```

The signed Packet Tunnel is not launched as part of source or build
verification. Physical-device validation remains a separate user-approved
step.

## Project Structure

```text
clients/ios/App/                         SwiftUI application and system VPN UI
clients/ios/Core/Sources/NeProtoCore/    Testable presentation and profile logic
clients/ios/Core/Tests/                  Swift unit tests
clients/ios/PacketTunnel/                Existing NetworkExtension data plane host
mobile/np2mobile/                        Existing shared Go client core
```

## Code Style

Use native composition and semantic styles rather than custom cards:

```swift
List {
    Section {
        Label("Подключение", systemImage: "network.badge.shield.half.filled")
        Toggle("VPN", isOn: connectionBinding)
    }
}
.listStyle(.insetGrouped)
```

- system typography and semantic colors;
- system `List`, `Section`, `Toggle`, `Button`, and SF Symbols;
- short focused views;
- VoiceOver labels for icon-only controls and VPN state;
- no fixed light/dark palette and no Flutter visual emulation.

## Testing Strategy

- Unit-test deterministic subscription grouping, titles, location emoji, and
  server ordering in `NeProtoCore`.
- Re-run the existing `NeProtoCore` test suite.
- Generate the Xcode project so new source files are included.
- Compile an unsigned iPhone Simulator build.
- Treat a signed physical-iPhone run as the final runtime gate.

## Boundaries

### Always

- Preserve secrets in Keychain and existing import validation.
- Preserve VPN lifecycle, cluster catalog synchronization, and status handling.
- Render unavailable cluster nodes as unavailable and non-selectable.
- Keep all interactive controls accessible.

### Ask first

- Changes to the NP/2 wire protocol or server.
- Changes to profile/import formats.
- New dependencies, analytics, or external services.
- Installing or launching the signed application on a physical device.

### Never

- Put secrets or import URIs in source, tests, logs, or screenshots.
- Disable TLS validation.
- Launch the Windows VPN client on this machine.
- Replace the Windows Flutter UI in this task.

## Success Criteria

- iOS starts on the new native SwiftUI home screen.
- The header shows `NeProto` and an accessible QR scanner button.
- The VPN section shows the selected server state and toggles the existing
  system VPN configuration.
- Profiles are grouped into subscriptions and every server row displays a
  location emoji with `🌐` fallback.
- Selecting a server updates the target of the VPN toggle.
- QR import uses the existing `np2://import/v2` pipeline and updates the list.
- Core Swift tests pass and the unsigned iOS build succeeds on Mac.

## Deferred

- A replacement native UI for routes and diagnostics.
- New server-management controls or manual profile editing.
- Visual customization beyond native iOS components.
