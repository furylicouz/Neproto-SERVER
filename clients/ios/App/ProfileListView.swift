import NeProtoCore
import NetworkExtension
import SwiftUI

struct ProfileListView: View {
    @EnvironmentObject private var profileStore: ProfileStore
    @EnvironmentObject private var vpnService: VPNService

    @State private var selectedProfileID: UUID?
    @State private var selectedSection = NeProtoSection.home
    @State private var isScanningQR = false
    @State private var clusterCatalogRefreshGate = ClusterCatalogRefreshGate()
    @State private var latencyMilliseconds: [UUID: Int] = [:]
    @State private var refreshingSubscriptionIDs: Set<String> = []
    @State private var pingingSubscriptionIDs: Set<String> = []

    var body: some View {
        TabView(selection: $selectedSection) {
            homeTab
                .tag(NeProtoSection.home)
                .tabItem {
                    Label(NeProtoSection.home.title, systemImage: NeProtoSection.home.systemImage)
                }

            profilesTab
                .tag(NeProtoSection.profiles)
                .tabItem {
                    Label(NeProtoSection.profiles.title, systemImage: NeProtoSection.profiles.systemImage)
                }

            diagnosticsTab
                .tag(NeProtoSection.diagnostics)
                .tabItem {
                    Label(NeProtoSection.diagnostics.title, systemImage: NeProtoSection.diagnostics.systemImage)
                }
        }
        .tint(NeProtoTheme.purple)
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
        .task(id: automaticPingTaskID) {
            await measureLatencies(for: subscriptions)
        }
        .alert("Ошибка", isPresented: errorIsPresented) {
            Button("ОК", role: .cancel) { vpnService.lastError = nil }
        } message: {
            Text(vpnService.lastError ?? "Неизвестная ошибка")
        }
    }

    private var homeTab: some View {
        NativeHomeView(
            subscriptions: subscriptions,
            selectedProfileID: selectedProfile?.id,
            selectedStatus: selectedStatus,
            isConnectionBusy: selectedProfile.map { vpnService.isBusy(profileID: $0.id) } ?? false,
            latencyMilliseconds: latencyMilliseconds,
            refreshingSubscriptionIDs: refreshingSubscriptionIDs,
            pingingSubscriptionIDs: pingingSubscriptionIDs,
            onConnectionChange: { _ in toggleSelectedProfile() },
            onSelectProfile: selectProfile,
            onRefreshSubscription: refreshSubscription,
            onPingSubscription: pingSubscription,
            onScanQR: { isScanningQR = true }
        )
    }

    private var profilesTab: some View {
        NavigationStack {
            ServerProfilesView(
                profiles: profileStore.profiles,
                selectedProfileID: selectedProfile?.id,
                status: vpnService.status,
                isBusy: vpnService.isBusy,
                onSelect: selectProfile,
                onToggle: toggleProfile,
                onDelete: deleteProfile,
                onAdd: { isScanningQR = true }
            )
            .navigationTitle("Профили")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { isScanningQR = true } label: {
                        Image(systemName: "qrcode.viewfinder")
                    }
                    .accessibilityLabel("Сканировать QR-код")
                }
            }
        }
    }

    private var diagnosticsTab: some View {
        NavigationStack {
            DiagnosticsView(
                lines: vpnService.diagnosticLog,
                onRefresh: vpnService.refreshProviderDiagnostics
            )
            .padding(.horizontal, 16)
            .navigationTitle("Диагностика")
        }
    }

    private var subscriptions: [NativeSubscriptionSection] {
        NativeHomePresentation.subscriptions(from: profileStore.profiles)
    }

    private var automaticPingTaskID: String {
        profileStore.profiles.map { profile in
            "\(profile.id.uuidString):\(profile.serverIdentity):\(profile.clusterAvailable)"
        }.joined(separator: "|")
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

    private func selectProfile(_ profile: ServerProfile) {
        guard profile.clusterAvailable else { return }
        selectedProfileID = profile.id
    }

    private func toggleSelectedProfile() {
        guard let selectedProfile else {
            isScanningQR = true
            return
        }
        vpnService.toggle(
            profile: selectedProfile,
            clientRoutes: profileStore.effectiveRoutes(for: selectedProfile.id)
        )
    }

    private func toggleProfile(_ profile: ServerProfile) {
        selectProfile(profile)
        vpnService.toggle(
            profile: profile,
            clientRoutes: profileStore.effectiveRoutes(for: profile.id)
        )
    }

    private func deleteProfile(_ profile: ServerProfile) {
        do {
            clusterCatalogRefreshGate.reset(profileID: profile.id)
            try profileStore.remove(profileID: profile.id)
            vpnService.removeConfiguration(profileID: profile.id)
            latencyMilliseconds.removeValue(forKey: profile.id)
            synchronizeSelection()
        } catch {
            vpnService.lastError = error.localizedDescription
        }
    }

    private func refreshSubscription(_ subscription: NativeSubscriptionSection) {
        guard !refreshingSubscriptionIDs.contains(subscription.id) else { return }
        guard let connectedProfile = subscription.profiles.first(where: {
            vpnService.status(for: $0.id) == .connected
        }) else {
            vpnService.lastError = "Для обновления подписки сначала подключитесь к одному из её серверов."
            return
        }

        refreshingSubscriptionIDs.insert(subscription.id)
        vpnService.recordDiagnostic("Обновление подписки NP/2: \(subscription.title)")
        vpnService.requestClusterCatalog(profileID: connectedProfile.id) { result in
            defer { refreshingSubscriptionIDs.remove(subscription.id) }
            do {
                let envelope = try result.get()
                let catalog = try profileStore.applyClusterCatalog(
                    envelope,
                    bootstrapProfileID: connectedProfile.id
                )
                vpnService.recordDiagnostic(
                    "Подписка NP/2 обновлена: ревизия \(catalog.revision), серверов \(catalog.servers.count)"
                )
                synchronizeSelection()
                vpnService.reload()
            } catch {
                vpnService.recordDiagnostic("Ошибка обновления подписки NP/2: \(error.localizedDescription)")
                vpnService.lastError = error.localizedDescription
            }
        }
    }

    private func pingSubscription(_ subscription: NativeSubscriptionSection) {
        Task { @MainActor in
            await measureLatencies(for: [subscription])
        }
    }

    @MainActor
    private func measureLatencies(for requestedSubscriptions: [NativeSubscriptionSection]) async {
        let pending = requestedSubscriptions.filter { subscription in
            !pingingSubscriptionIDs.contains(subscription.id) &&
                !NativeHomePresentation.pingableProfiles(in: subscription).isEmpty
        }
        guard !pending.isEmpty else { return }

        let subscriptionIDs = Set(pending.map(\.id))
        let profiles = pending.flatMap(NativeHomePresentation.pingableProfiles)
        pingingSubscriptionIDs.formUnion(subscriptionIDs)
        defer { pingingSubscriptionIDs.subtract(subscriptionIDs) }

        let measurements = await ServerLatencyProbe.measure(profiles: profiles)
        guard !Task.isCancelled else { return }
        for profile in profiles {
            if let latency = measurements[profile.id] {
                latencyMilliseconds[profile.id] = latency
            } else {
                latencyMilliseconds.removeValue(forKey: profile.id)
            }
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
