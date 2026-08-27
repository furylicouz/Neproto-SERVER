import NeProtoCore
import NetworkExtension
import SwiftUI

struct NativeHomeView: View {
    let subscriptions: [NativeSubscriptionSection]
    let selectedProfileID: UUID?
    let selectedStatus: NEVPNStatus
    let isConnectionBusy: Bool
    let onConnectionChange: (Bool) -> Void
    let onSelectProfile: (ServerProfile) -> Void
    let onScanQR: () -> Void

    private var hasSelectedProfile: Bool { selectedProfileID != nil }

    private var connectionIsActive: Bool {
        VPNStatusPresentation.isActive(selectedStatus)
    }

    var body: some View {
        NavigationStack {
            List {
                connectionSection
                subscriptionSections
            }
            .listStyle(.insetGrouped)
            .navigationTitle("NeProto")
            .navigationBarTitleDisplayMode(.large)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: onScanQR) {
                        Image(systemName: "qrcode.viewfinder")
                    }
                    .accessibilityLabel("Сканировать QR-код")
                    .accessibilityHint("Добавить подписку NP/2")
                }
            }
        }
    }

    private var connectionSection: some View {
        Section {
            Toggle(isOn: connectionBinding) {
                Label {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Подключение")
                            .font(.body)
                        Text(connectionStatusTitle)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                } icon: {
                    Image(systemName: "network.badge.shield.half.filled")
                        .foregroundStyle(connectionTint)
                }
            }
            .disabled(!hasSelectedProfile || isConnectionBusy)
            .accessibilityLabel("VPN")
            .accessibilityValue(connectionStatusTitle)
            .accessibilityHint(connectionAccessibilityHint)
        } footer: {
            if !hasSelectedProfile {
                Text("Отсканируйте подписку, чтобы выбрать сервер.")
            }
        }
    }

    @ViewBuilder
    private var subscriptionSections: some View {
        if subscriptions.isEmpty {
            Section("Подписка") {
                Button(action: onScanQR) {
                    Label("Отсканировать QR-код", systemImage: "qrcode.viewfinder")
                }
                .accessibilityHint("Импортировать подписку NP/2")
            }
        } else {
            ForEach(subscriptions) { subscription in
                Section {
                    ForEach(subscription.profiles) { profile in
                        NativeServerRow(
                            profile: profile,
                            isSelected: profile.id == selectedProfileID,
                            onSelect: { onSelectProfile(profile) }
                        )
                    }
                } header: {
                    Text(subscriptionHeader(subscription.title))
                }
            }
        }
    }

    private var connectionBinding: Binding<Bool> {
        Binding(
            get: { connectionIsActive },
            set: { requestedState in
                guard requestedState != connectionIsActive else { return }
                onConnectionChange(requestedState)
            }
        )
    }

    private var connectionStatusTitle: String {
        guard hasSelectedProfile else { return "Нет выбранного сервера" }
        return VPNStatusPresentation.title(for: selectedStatus, isBusy: isConnectionBusy)
    }

    private var connectionTint: Color {
        guard hasSelectedProfile else { return .secondary }
        return VPNStatusPresentation.tint(for: selectedStatus)
    }

    private var connectionAccessibilityHint: String {
        guard hasSelectedProfile else { return "Сначала добавьте подписку" }
        return connectionIsActive ? "Отключить VPN" : "Подключить VPN"
    }

    private func subscriptionHeader(_ title: String) -> String {
        title == "Серверы" ? "Подписка" : "Подписка · \(title)"
    }
}

private struct NativeServerRow: View {
    let profile: ServerProfile
    let isSelected: Bool
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: 12) {
                Text(NativeHomePresentation.locationEmoji(for: profile))
                    .font(.title2)
                    .frame(width: 34)
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 2) {
                    Text(profile.name)
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    Text(profile.serverIdentity)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 8)

                if !profile.clusterAvailable {
                    Text("Недоступен")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                if isSelected {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(Color.accentColor)
                        .accessibilityHidden(true)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!profile.clusterAvailable)
        .opacity(profile.clusterAvailable ? 1 : 0.55)
        .accessibilityLabel(serverAccessibilityLabel)
        .accessibilityValue(isSelected ? "Выбран" : "Не выбран")
        .accessibilityHint(profile.clusterAvailable ? "Выбрать сервер" : "Сервер временно недоступен")
    }

    private var serverAccessibilityLabel: String {
        let location = NativeHomePresentation.locationEmoji(for: profile)
        return "\(location) \(profile.name), \(profile.serverIdentity)"
    }
}
