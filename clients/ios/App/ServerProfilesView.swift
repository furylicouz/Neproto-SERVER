import NeProtoCore
import NetworkExtension
import SwiftUI
import UIKit

private enum ServerLatencyProbe {
    static func measure(profile: ServerProfile) async -> Int? {
        guard let url = URL(string: "https://\(profile.serverIdentity)/") else { return nil }

        var request = URLRequest(url: url)
        request.httpMethod = "HEAD"
        request.timeoutInterval = 3
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData

        let startedAt = Date()
        do {
            _ = try await URLSession.shared.bytes(for: request)
            return max(0, Int(Date().timeIntervalSince(startedAt) * 1_000))
        } catch {
            return nil
        }
    }
}

struct ServerProfilesView: View {
    let profiles: [ServerProfile]
    let selectedProfileID: UUID?
    let status: (UUID) -> NEVPNStatus
    let isBusy: (UUID) -> Bool
    let onSelect: (ServerProfile) -> Void
    let onToggle: (ServerProfile) -> Void
    let onDelete: (ServerProfile) -> Void
    let onAdd: () -> Void

    @State private var latencyMilliseconds: [UUID: Int] = [:]
    @State private var profilePendingDeletion: ServerProfile?

    var body: some View {
        Group {
            if profiles.isEmpty {
                emptyState
            } else {
                serverList
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var serverList: some View {
        List {
            Section {
                ForEach(profiles) { profile in
                    ServerProfileRow(
                        profile: profile,
                        latencyMilliseconds: latencyMilliseconds[profile.id],
                        isSelected: selectedProfileID == profile.id,
                        onSelect: { onSelect(profile) }
                    )
                    .listRowBackground(Color(uiColor: .secondarySystemGroupedBackground))
                    .swipeActions(edge: .leading, allowsFullSwipe: true) {
                        Button {
                            onToggle(profile)
                        } label: {
                            Label(
                                VPNStatusPresentation.isActive(status(profile.id)) ? "Отключить" : "Подключить",
                                systemImage: VPNStatusPresentation.isActive(status(profile.id)) ? "stop.fill" : "play.fill"
                            )
                        }
                        .tint(VPNStatusPresentation.isActive(status(profile.id)) ? .red : NeProtoTheme.purple)
                        .disabled(isBusy(profile.id) || !profile.clusterAvailable)
                    }
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        Button(role: .destructive) {
                            profilePendingDeletion = profile
                        } label: {
                            Label("Удалить", systemImage: "trash")
                        }
                    }
                }
            } footer: {
                Text("Задержка измеряется до HTTPS-входа каждого NP/2-сервера.")
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .refreshable {
            await refreshLatencies()
        }
        .task(id: probeTaskID) {
            while !Task.isCancelled {
                await refreshLatencies()
                try? await Task.sleep(nanoseconds: 15_000_000_000)
            }
        }
        .confirmationDialog(
            deletionTitle,
            isPresented: deletionIsPresented,
            titleVisibility: .visible
        ) {
            Button("Удалить", role: .destructive) {
                guard let profilePendingDeletion else { return }
                onDelete(profilePendingDeletion)
                self.profilePendingDeletion = nil
            }
            Button("Отмена", role: .cancel) {
                profilePendingDeletion = nil
            }
        }
    }

    private var emptyState: some View {
        VStack(spacing: 16) {
            Image(systemName: "network.badge.shield.half.filled")
                .font(.system(size: 48, weight: .light))
                .foregroundStyle(NeProtoTheme.purpleLight)
            Text("Нет серверов")
                .font(.title2.weight(.semibold))
            Text("Добавьте NP/2-сервер, чтобы создать системное VPN-подключение.")
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("Добавить сервер", action: onAdd)
                .buttonStyle(.borderedProminent)
                .tint(NeProtoTheme.purple)
        }
        .padding()
    }

    private var probeTaskID: String {
        profiles.map { "\($0.id.uuidString):\($0.serverIdentity)" }.joined(separator: "|")
    }

    private var deletionTitle: String {
        guard let profilePendingDeletion else { return "Удалить сервер?" }
        return "Удалить сервер «\(profilePendingDeletion.name)»?"
    }

    private var deletionIsPresented: Binding<Bool> {
        Binding(
            get: { profilePendingDeletion != nil },
            set: { if !$0 { profilePendingDeletion = nil } }
        )
    }

    @MainActor
    private func refreshLatencies() async {
        let availableProfiles = profiles.filter(\.clusterAvailable)
        for batchStart in stride(from: 0, to: availableProfiles.count, by: 4) {
            guard !Task.isCancelled else { return }
            let batchEnd = min(batchStart + 4, availableProfiles.count)
            let batch = availableProfiles[batchStart..<batchEnd]

            await withTaskGroup(of: (UUID, Int?).self) { group in
                for profile in batch {
                    group.addTask {
                        (profile.id, await ServerLatencyProbe.measure(profile: profile))
                    }
                }

                for await (profileID, latency) in group {
                    if let latency {
                        latencyMilliseconds[profileID] = latency
                    } else {
                        latencyMilliseconds.removeValue(forKey: profileID)
                    }
                }
            }
        }
    }
}

private struct ServerProfileRow: View {
    let profile: ServerProfile
    let latencyMilliseconds: Int?
    let isSelected: Bool
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: 12) {
                Text(locationIcon)
                    .font(.title2)
                    .frame(width: 36, height: 36)
                    .background(NeProtoTheme.purple.opacity(0.14), in: Circle())
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 2) {
                    Text(profile.name)
                        .font(.body)
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    Text(profile.serverIdentity)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 8)

                HStack(spacing: 8) {
                    ServerSignalBars(activeBars: signalBars)
                    Text(ServerLatencyPresentation.text(milliseconds: latencyMilliseconds))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .frame(minWidth: 48, alignment: .trailing)
                }

                if isSelected {
                    Image(systemName: "checkmark")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(NeProtoTheme.purpleLight)
                        .accessibilityLabel("Выбран")
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!profile.clusterAvailable)
        .opacity(profile.clusterAvailable ? 1 : 0.5)
        .accessibilityLabel("\(profile.name), \(ServerLatencyPresentation.text(milliseconds: latencyMilliseconds))")
        .accessibilityHint("Выбрать сервер")
    }

    private var locationIcon: String {
        ServerLocationPresentation.flag(
            forRegion: profile.region,
            fallbackCountryCode: profile.serverIdentity == "neproto.lyntragram.ru" ? "RU" : nil
        ) ?? "🌐"
    }

    private var signalBars: Int {
        guard profile.clusterAvailable else { return 0 }
        return ServerLatencyPresentation.bars(milliseconds: latencyMilliseconds)
    }
}

private struct ServerSignalBars: View {
    let activeBars: Int

    var body: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(0..<4, id: \.self) { index in
                Capsule()
                    .fill(index < activeBars ? NeProtoTheme.purpleLight : Color.secondary.opacity(0.24))
                    .frame(width: 3, height: CGFloat(5 + index * 3))
            }
        }
        .frame(width: 20, height: 16, alignment: .bottom)
        .accessibilityHidden(true)
    }
}
