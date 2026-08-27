import NeProtoCore
import NetworkExtension
import SwiftUI

struct NativeHomeView: View {
    let subscriptions: [NativeSubscriptionSection]
    let selectedProfileID: UUID?
    let selectedStatus: NEVPNStatus
    let isConnectionBusy: Bool
    let latencyMilliseconds: [UUID: Int]
    let refreshingSubscriptionIDs: Set<String>
    let pingingSubscriptionIDs: Set<String>
    let onConnectionChange: (Bool) -> Void
    let onSelectProfile: (ServerProfile) -> Void
    let onRefreshSubscription: (NativeSubscriptionSection) -> Void
    let onPingSubscription: (NativeSubscriptionSection) -> Void
    let onScanQR: () -> Void

    @State private var disclosureState = NativeSubscriptionDisclosureState()

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
                    subscriptionControlRow(subscription)

                    if disclosureState.isExpanded(subscriptionID: subscription.id) {
                        ForEach(subscription.profiles) { profile in
                            NativeServerRow(
                                profile: profile,
                                latencyMilliseconds: latencyMilliseconds[profile.id],
                                isPinging: pingingSubscriptionIDs.contains(subscription.id),
                                isSelected: profile.id == selectedProfileID,
                                onSelect: { onSelectProfile(profile) }
                            )
                        }
                    }
                }
            }
        }
    }

    private func subscriptionControlRow(_ subscription: NativeSubscriptionSection) -> some View {
        HStack(spacing: 8) {
            Button {
                withAnimation(.easeInOut(duration: 0.2)) {
                    disclosureState.toggle(subscriptionID: subscription.id)
                }
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: disclosureState.isExpanded(subscriptionID: subscription.id)
                        ? "chevron.down"
                        : "chevron.right")
                        .font(.body.weight(.semibold))
                        .foregroundStyle(NeProtoTheme.purple)
                        .frame(width: 28)

                    VStack(alignment: .leading, spacing: 2) {
                        Text("Подписка")
                            .font(.body.weight(.medium))
                            .foregroundStyle(.primary)
                        if subscription.title != "Серверы" {
                            Text(subscription.title)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }

                    Spacer(minLength: 4)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Подписка \(subscription.title)")
            .accessibilityValue(disclosureState.isExpanded(subscriptionID: subscription.id) ? "Раскрыта" : "Закрыта")
            .accessibilityHint("Показать или скрыть серверы подписки")

            subscriptionActionButton(
                systemImage: "arrow.clockwise",
                accessibilityLabel: "Обновить подписку",
                isRunning: refreshingSubscriptionIDs.contains(subscription.id),
                isDisabled: !subscriptionSupportsRefresh(subscription)
            ) {
                onRefreshSubscription(subscription)
            }

            subscriptionActionButton(
                systemImage: "network",
                accessibilityLabel: "Проверить задержку серверов",
                isRunning: pingingSubscriptionIDs.contains(subscription.id),
                isDisabled: subscription.profiles.allSatisfy { !$0.clusterAvailable }
            ) {
                onPingSubscription(subscription)
            }
        }
    }

    private func subscriptionActionButton(
        systemImage: String,
        accessibilityLabel: String,
        isRunning: Bool,
        isDisabled: Bool,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Group {
                if isRunning {
                    ProgressView()
                        .controlSize(.small)
                } else {
                    Image(systemName: systemImage)
                }
            }
            .frame(width: 30, height: 30)
        }
        .buttonStyle(.borderless)
        .disabled(isRunning || isDisabled)
        .accessibilityLabel(accessibilityLabel)
    }

    private func subscriptionSupportsRefresh(_ subscription: NativeSubscriptionSection) -> Bool {
        subscription.profiles.contains { profile in
            profile.clusterID != nil && profile.catalogPublicKey != nil
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

}

private struct NativeServerRow: View {
    let profile: ServerProfile
    let latencyMilliseconds: Int?
    let isPinging: Bool
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

                if let latencyMilliseconds {
                    Text(ServerLatencyPresentation.text(milliseconds: latencyMilliseconds))
                        .font(.caption.monospacedDigit().weight(.medium))
                        .foregroundStyle(.green)
                        .accessibilityLabel("Задержка \(latencyMilliseconds) миллисекунд")
                } else if isPinging, profile.clusterAvailable {
                    ProgressView()
                        .controlSize(.small)
                        .accessibilityLabel("Измерение задержки")
                }

                if isSelected {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(NeProtoTheme.purple)
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
