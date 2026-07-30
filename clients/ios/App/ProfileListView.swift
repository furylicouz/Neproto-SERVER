import NeProtoCore
import NetworkExtension
import SwiftUI

struct ProfileListView: View {
    @EnvironmentObject private var profileStore: ProfileStore
    @EnvironmentObject private var vpnService: VPNService

    @State private var selectedSection = NeProtoSection.home
    @State private var selectedProfileID: UUID?
    @State private var isShowingServers = false
    @State private var isAddingProfile = false
    @State private var isScanningQR = false
    @State private var clusterCatalogRefreshGate = ClusterCatalogRefreshGate()

    var body: some View {
        ZStack {
            NeProtoTheme.background
                .ignoresSafeArea()

            nativeTabs
        }
        .tint(NeProtoTheme.purple)
        .animation(.easeInOut(duration: 0.18), value: selectedSection)
        .sheet(isPresented: $isAddingProfile) {
            ProfileEditorView { profile, secret in
                try profileStore.save(profile: profile, secret: secret)
                selectedProfileID = profile.id
                vpnService.reload()
            }
        }
        .sheet(isPresented: $isScanningQR) {
            QRScannerView(completion: handleQRResult)
        }
        .onAppear {
            synchronizeSelection()
            vpnService.reload {
                synchronizeConnectedClusters()
            }
        }
        .onChange(of: profileStore.profiles.map(\.id)) { _ in
            synchronizeSelection()
        }
        .onReceive(NotificationCenter.default.publisher(for: .NEVPNStatusDidChange)) { _ in
            vpnService.handleStatusChange()
            synchronizeConnectedClusters()
        }
        .task(id: selectedProfile?.id) {
            guard let profileID = selectedProfile?.id else { return }
            while !Task.isCancelled {
                vpnService.refreshLiveMetrics(profileID: profileID)
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
        .alert("Ошибка", isPresented: errorIsPresented) {
            Button("ОК", role: .cancel) { vpnService.lastError = nil }
        } message: {
            Text(vpnService.lastError ?? "Неизвестная ошибка")
        }
    }

    @ViewBuilder
    private var nativeTabs: some View {
        if #available(iOS 26.0, *) {
            tabView
                .tabBarMinimizeBehavior(.onScrollDown)
        } else {
            tabView
        }
    }

    private var tabView: some View {
        TabView(selection: $selectedSection) {
            navigationPage(for: .home) {
                homeContent
            }
            .tabItem {
                Label(NeProtoSection.home.title, systemImage: NeProtoSection.home.systemImage)
            }
            .tag(NeProtoSection.home)

            navigationPage(for: .routes) {
                ClusterRoutesView(profile: selectedProfile) { error in
                    vpnService.lastError = error.localizedDescription
                }
            }
            .tabItem {
                Label(NeProtoSection.routes.title, systemImage: NeProtoSection.routes.systemImage)
            }
            .tag(NeProtoSection.routes)

            navigationPage(for: .diagnostics) {
                diagnosticsContent
            }
            .tabItem {
                Label(NeProtoSection.diagnostics.title, systemImage: NeProtoSection.diagnostics.systemImage)
            }
            .tag(NeProtoSection.diagnostics)
        }
    }

    private func navigationPage<Content: View>(
        for section: NeProtoSection,
        @ViewBuilder content: () -> Content
    ) -> some View {
        NavigationStack {
            content()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(.horizontal, section == .routes ? 0 : 24)
                .navigationTitle("")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    leadingNavigationTitle(section == .home ? "NeProto" : section.title)
                    ToolbarItem(placement: .topBarTrailing) {
                        AddServerMenu(
                            onAddServer: { isAddingProfile = true },
                            onScanQR: { isScanningQR = true }
                        )
                    }
                }
        }
    }

    @ViewBuilder
    private var homeContent: some View {
        VPNHomeView(
            profile: selectedProfile,
            status: selectedStatus,
            isBusy: selectedProfile.map { vpnService.isBusy(profileID: $0.id) } ?? false,
            traffic: selectedProfile.map { vpnService.traffic(for: $0.id) } ?? LiveTrafficMetrics(),
            connectedSince: selectedProfile.flatMap { vpnService.connectedSince(profileID: $0.id) },
            onChooseServer: showServerPicker,
            onToggle: toggleSelectedProfile
        )
        .navigationDestination(isPresented: $isShowingServers) {
            serversContent
                .navigationTitle("")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    leadingNavigationTitle("Серверы")
                    ToolbarItem(placement: .topBarTrailing) {
                        AddServerMenu(
                            onAddServer: { isAddingProfile = true },
                            onScanQR: { isScanningQR = true }
                        )
                    }
                }
        }
    }

    private var serversContent: some View {
        ServerProfilesView(
            profiles: profileStore.profiles,
            selectedProfileID: selectedProfile?.id,
            status: { vpnService.status(for: $0) },
            isBusy: { vpnService.isBusy(profileID: $0) },
            onSelect: selectProfile,
            onToggle: {
                vpnService.toggle(
                    profile: $0,
                    clientRoutes: profileStore.effectiveRoutes(for: $0.id)
                )
            },
            onDelete: deleteProfile,
            onAdd: { isAddingProfile = true }
        )
    }

    private var diagnosticsContent: some View {
        DiagnosticsView(
            lines: vpnService.diagnosticLog,
            onRefresh: vpnService.refreshProviderDiagnostics
        )
    }

    @ToolbarContentBuilder
    private func leadingNavigationTitle(_ title: String) -> some ToolbarContent {
        if #available(iOS 26.0, *) {
            ToolbarItem(placement: .topBarLeading) {
                Text(title)
            }
            .sharedBackgroundVisibility(.hidden)
        } else {
            ToolbarItem(placement: .topBarLeading) {
                Text(title)
            }
        }
    }

    private var selectedProfile: ServerProfile? {
        if let selectedProfileID,
           let selected = profileStore.profiles.first(where: { $0.id == selectedProfileID }) {
            return selected
        }
        return profileStore.profiles.first
    }

    private var selectedStatus: NEVPNStatus {
        selectedProfile.map { vpnService.status(for: $0.id) } ?? .disconnected
    }

    private var errorIsPresented: Binding<Bool> {
        Binding(
            get: { vpnService.lastError != nil },
            set: { if !$0 { vpnService.lastError = nil } }
        )
    }

    private func synchronizeSelection() {
        guard !profileStore.profiles.isEmpty else {
            selectedProfileID = nil
            return
        }
        if let selectedProfileID,
           profileStore.profiles.contains(where: { $0.id == selectedProfileID }) {
            return
        }
        selectedProfileID = profileStore.profiles.first?.id
    }

    private func showServerPicker() {
        isShowingServers = true
    }

    private func selectProfile(_ profile: ServerProfile) {
        selectedProfileID = profile.id
        isShowingServers = false
    }

    private func toggleSelectedProfile() {
        guard let selectedProfile else {
            isAddingProfile = true
            return
        }
        vpnService.toggle(
            profile: selectedProfile,
            clientRoutes: profileStore.effectiveRoutes(for: selectedProfile.id)
        )
    }

    private func deleteProfile(_ profile: ServerProfile) {
        clusterCatalogRefreshGate.reset(profileID: profile.id)
        vpnService.removeConfiguration(profileID: profile.id)
        do {
            try profileStore.remove(profileID: profile.id)
            synchronizeSelection()
        } catch {
            vpnService.lastError = error.localizedDescription
        }
    }

    private func handleQRResult(_ result: Result<String, Error>) {
        defer { isScanningQR = false }
        do {
            switch result {
            case let .success(uri):
                let profile = try profileStore.importOnboardingURI(uri)
                selectedProfileID = profile.id
                selectedSection = .home
                vpnService.reload {
                    synchronizeConnectedClusters()
                }
            case let .failure(error):
                throw error
            }
        } catch {
            vpnService.lastError = error.localizedDescription
        }
    }

    private func synchronizeConnectedClusters() {
        for profile in profileStore.profiles {
            guard profile.clusterID != nil,
                  profile.catalogPublicKey != nil,
                  let sessionID = vpnService.clusterCatalogSessionID(profileID: profile.id),
                  clusterCatalogRefreshGate.shouldStart(
                      profileID: profile.id,
                      sessionID: sessionID
                  ) else {
                continue
            }
            vpnService.recordDiagnostic("Запрос каталога NP/2: \(profile.serverIdentity)")
            vpnService.requestClusterCatalog(profileID: profile.id) { result in
                do {
                    let envelope = try result.get()
                    let catalog = try profileStore.applyClusterCatalog(envelope, bootstrapProfileID: profile.id)
                    clusterCatalogRefreshGate.finish(
                        profileID: profile.id,
                        sessionID: sessionID,
                        succeeded: true
                    )
                    vpnService.recordDiagnostic(
                        "Каталог NP/2 синхронизирован: ревизия \(catalog.revision), серверов \(catalog.servers.count)"
                    )
                    synchronizeSelection()
                    vpnService.reload()
                } catch {
                    clusterCatalogRefreshGate.finish(
                        profileID: profile.id,
                        sessionID: sessionID,
                        succeeded: false
                    )
                    vpnService.recordDiagnostic("Ошибка каталога NP/2: \(error.localizedDescription)")
                    vpnService.lastError = error.localizedDescription
                }
            }
        }
    }
}
