import NeProtoCore
import NetworkExtension
import SwiftUI

struct ProfileListView: View {
    @EnvironmentObject private var profileStore: ProfileStore
    @EnvironmentObject private var vpnService: VPNService

    @State private var selectedProfileID: UUID?
    @State private var isScanningQR = false
    @State private var clusterCatalogRefreshGate = ClusterCatalogRefreshGate()

    var body: some View {
        NativeHomeView(
            subscriptions: NativeHomePresentation.subscriptions(from: profileStore.profiles),
            selectedProfileID: selectedProfile?.id,
            selectedStatus: selectedStatus,
            isConnectionBusy: selectedProfile.map { vpnService.isBusy(profileID: $0.id) } ?? false,
            onConnectionChange: { _ in toggleSelectedProfile() },
            onSelectProfile: selectProfile,
            onScanQR: { isScanningQR = true }
        )
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

    private func handleQRResult(_ result: Result<String, Error>) {
        defer { isScanningQR = false }
        do {
            switch result {
            case let .success(uri):
                let profile = try profileStore.importOnboardingURI(uri)
                selectedProfileID = profile.id
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
