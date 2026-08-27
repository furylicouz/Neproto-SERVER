import SwiftUI

@main
struct NeProtoApp: App {
    @StateObject private var profileStore = ProfileStore()
    @StateObject private var vpnService = VPNService()

    var body: some Scene {
        WindowGroup {
            ProfileListView()
                .environmentObject(profileStore)
                .environmentObject(vpnService)
                .tint(NeProtoTheme.purple)
        }
    }
}
