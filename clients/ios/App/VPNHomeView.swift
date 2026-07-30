import NeProtoCore
import NetworkExtension
import SwiftUI

struct VPNHomeView: View {
    let profile: ServerProfile?
    let status: NEVPNStatus
    let isBusy: Bool
    let traffic: LiveTrafficMetrics
    let connectedSince: Date?
    let onChooseServer: () -> Void
    let onToggle: () -> Void

    private var isConnected: Bool { status == .connected }

    var body: some View {
        GeometryReader { proxy in
            ScrollView(showsIndicators: false) {
                VStack(spacing: 0) {
                    serverCard

                    ConnectionMap(isConnected: isConnected, showsMarker: profile != nil)
                        .frame(maxWidth: .infinity)
                        .aspectRatio(338 / 217, contentMode: .fit)
                        .padding(.top, 26)

                    speedRow
                        .padding(.top, 24)

                    connectButton
                        .padding(.top, 24)

                    Spacer(minLength: 20)
                }
                .frame(maxWidth: .infinity)
                .frame(minHeight: proxy.size.height)
            }
        }
    }

    private var serverCard: some View {
        Button(action: onChooseServer) {
            HStack(spacing: 14) {
                Text(locationIcon)
                    .font(.system(size: 25))
                    .frame(width: 42, height: 42)
                    .background(NeProtoTheme.purple.opacity(0.18), in: Circle())
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 3) {
                    Text(locationTitle)
                        .font(.headline)
                        .foregroundStyle(NeProtoTheme.primaryText)
                        .lineLimit(1)
                    Text(profileAddress)
                        .font(.caption.monospaced())
                        .foregroundStyle(NeProtoTheme.primaryText.opacity(0.78))
                        .lineLimit(1)
                }

                Spacer(minLength: 8)
                if profile != nil {
                    signalBars
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(NeProtoTheme.secondaryText)
            }
            .padding(.horizontal, 16)
            .frame(maxWidth: .infinity, minHeight: 68)
            .contentShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
            .neProtoGlassCard(
                cornerRadius: 20,
                tint: NeProtoTheme.purple.opacity(0.12),
                interactive: true
            )
        }
        .buttonStyle(.plain)
        .accessibilityLabel(profile == nil ? "Добавить сервер" : "Выбрать сервер, сейчас \(locationTitle)")
    }

    @ViewBuilder
    private var speedRow: some View {
        if #available(iOS 26.0, *) {
            GlassEffectContainer(spacing: 12) {
                speedMetrics
            }
        } else {
            speedMetrics
        }
    }

    private var speedMetrics: some View {
        HStack(spacing: 12) {
            SpeedMetric(
                title: "Входящая",
                systemImage: "arrow.down",
                rate: DashboardPresentation.rate(bytesPerSecond: traffic.downloadBytesPerSecond)
            )
            SpeedMetric(
                title: "Исходящая",
                systemImage: "arrow.up",
                rate: DashboardPresentation.rate(bytesPerSecond: traffic.uploadBytesPerSecond)
            )
        }
    }

    private var connectButton: some View {
        Button(action: onToggle) {
            ZStack {
                Circle()
                    .fill(powerTint.opacity(profile == nil ? 0.22 : 0.86))
                    .shadow(color: powerTint.opacity(0.34), radius: 18, y: 9)

                VStack(spacing: 8) {
                    if isBusy {
                        ProgressView()
                            .tint(.white)
                            .scaleEffect(1.2)
                            .frame(height: 38)
                    } else {
                        Image(systemName: "power")
                            .font(.system(size: 38, weight: .light))
                            .foregroundStyle(.white)
                            .frame(height: 38)
                    }

                    Text(connectTitle)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.white)
                        .lineLimit(1)

                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        Text(connectionDuration(at: context.date))
                            .font(.caption2.monospacedDigit())
                            .foregroundStyle(.white.opacity(0.78))
                    }
                }
            }
            .frame(width: 142, height: 142)
            .contentShape(Circle())
            .neProtoGlassCircle(tint: powerTint, interactive: !isBusy)
        }
        .buttonStyle(.plain)
        .disabled(isBusy)
        .accessibilityLabel(connectAccessibilityLabel)
        .accessibilityValue(connectionDuration(at: .now))
        .accessibilityHint(profile == nil ? "Откроет форму нового сервера" : "Двойное касание изменит состояние VPN")
    }

    private var signalBars: some View {
        HStack(alignment: .bottom, spacing: 3) {
            ForEach(0..<4, id: \.self) { index in
                Capsule()
                    .fill(index < activeSignalBars ? NeProtoTheme.purpleLight : NeProtoTheme.secondaryText.opacity(0.28))
                    .frame(width: 4, height: CGFloat(7 + index * 4))
            }
        }
        .frame(width: 30, height: 24, alignment: .bottom)
        .accessibilityHidden(true)
    }

    private var isMoscowNode: Bool {
        profile?.serverIdentity == "neproto.lyntragram.ru"
    }

    private var locationIcon: String {
        guard let profile else { return "＋" }
        return ServerLocationPresentation.flag(
            forRegion: profile.region,
            fallbackCountryCode: isMoscowNode ? "RU" : nil
        ) ?? "🌐"
    }

    private var locationTitle: String {
        guard let profile else { return "Добавьте сервер" }
        if isMoscowNode {
            return "Россия · Москва"
        }
        return ServerLocationPresentation.title(forRegion: profile.region, fallbackName: profile.name)
    }

    private var profileAddress: String {
        guard let profile else { return "Настройте профиль NP/2" }
        return profile.serverAddress ?? profile.serverIdentity
    }

    private var activeSignalBars: Int {
        switch status {
        case .connected: 4
        case .connecting, .reasserting: 3
        default: 1
        }
    }

    private var connectTitle: String {
        guard profile != nil else { return "Добавить сервер" }
        return VPNStatusPresentation.title(for: status, isBusy: isBusy)
    }

    private var powerTint: Color {
        guard profile != nil else { return NeProtoTheme.secondaryText }
        if status == .disconnected {
            return NeProtoTheme.purple
        }
        return VPNStatusPresentation.tint(for: status)
    }

    private var connectAccessibilityLabel: String {
        guard let profile else { return "Добавить сервер" }
        let action = VPNStatusPresentation.isActive(status) ? "Отключить" : "Подключить"
        return "\(action) \(profile.name)"
    }

    private func connectionDuration(at date: Date) -> String {
        guard isConnected, let connectedSince else { return "00:00:00" }
        return DashboardPresentation.duration(seconds: Int(max(0, date.timeIntervalSince(connectedSince))))
    }
}

private struct ConnectionMap: View {
    let isConnected: Bool
    let showsMarker: Bool

    var body: some View {
        ZStack {
            Image("WorldMap")
                .resizable()
                .aspectRatio(338 / 217, contentMode: .fit)
                .accessibilityHidden(true)

            if showsMarker {
                TimelineView(.animation(minimumInterval: 1.0 / 30.0, paused: !isConnected)) { timeline in
                    GeometryReader { proxy in
                        let phase = isConnected
                            ? (sin(timeline.date.timeIntervalSinceReferenceDate * 2.6) + 1) / 2
                            : 0
                        ZStack {
                            Circle()
                                .fill(NeProtoTheme.purple.opacity(isConnected ? 0.24 * (1 - phase) : 0.12))
                                .frame(width: 30 + 28 * phase, height: 30 + 28 * phase)
                            Circle()
                                .fill(isConnected ? NeProtoTheme.purpleLight : NeProtoTheme.secondaryText)
                                .frame(width: 9, height: 9)
                                .overlay(Circle().stroke(NeProtoTheme.background, lineWidth: 2))
                        }
                        .position(x: proxy.size.width * 0.61, y: proxy.size.height * 0.28)
                    }
                }
                .allowsHitTesting(false)
                .accessibilityHidden(true)
            }
        }
        .accessibilityElement()
        .accessibilityLabel(isConnected ? "Соединение с сервером активно" : "Соединение с сервером неактивно")
    }
}

private struct SpeedMetric: View {
    let title: String
    let systemImage: String
    let rate: FormattedTrafficRate

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            Label(title, systemImage: systemImage)
                .font(.caption.weight(.medium))
                .foregroundStyle(NeProtoTheme.secondaryText)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(rate.value)
                    .font(.title3.bold().monospacedDigit())
                Text(rate.unit)
                    .font(.caption2)
            }
            .foregroundStyle(NeProtoTheme.primaryText)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .neProtoGlassCard(cornerRadius: 18, tint: NeProtoTheme.purple.opacity(0.08))
        .accessibilityElement(children: .combine)
    }
}
