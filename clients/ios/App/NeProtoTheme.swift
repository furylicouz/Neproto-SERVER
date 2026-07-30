import NetworkExtension
import SwiftUI
import UIKit

enum NeProtoTheme {
    static let background = Color(uiColor: .systemBackground)
    static let surface = Color(uiColor: .secondarySystemBackground)
    static let surfaceMuted = Color(uiColor: .tertiarySystemFill)
    static let purple = Color(red: 102 / 255, green: 34 / 255, blue: 204 / 255)
    static let purpleLight = Color(red: 174 / 255, green: 119 / 255, blue: 255 / 255)
    static let connected = Color(red: 40 / 255, green: 190 / 255, blue: 104 / 255)
    static let transitioning = Color(red: 255 / 255, green: 149 / 255, blue: 0)
    static let map = Color(uiColor: .systemGray4)
    static let primaryText = Color.primary
    static let secondaryText = Color.secondary
}

enum VPNStatusPresentation {
    static func isActive(_ status: NEVPNStatus) -> Bool {
        status == .connected || status == .connecting || status == .reasserting || status == .disconnecting
    }

    static func title(for status: NEVPNStatus, isBusy: Bool = false) -> String {
        if isBusy {
            return "Настройка…"
        }
        return switch status {
        case .connected: "Подключено"
        case .connecting: "Подключение…"
        case .disconnecting: "Отключение…"
        case .reasserting: "Переподключение…"
        case .invalid: "Требуется настройка"
        default: "Не подключено"
        }
    }

    static func tint(for status: NEVPNStatus) -> Color {
        switch status {
        case .connected: NeProtoTheme.connected
        case .connecting, .reasserting, .disconnecting: NeProtoTheme.transitioning
        case .invalid: .red
        default: NeProtoTheme.secondaryText
        }
    }
}

extension View {
    @ViewBuilder
    func neProtoGlassCard(
        cornerRadius: CGFloat,
        tint: Color? = nil,
        interactive: Bool = false
    ) -> some View {
        if #available(iOS 26.0, *) {
            glassEffect(
                .regular.tint(tint).interactive(interactive),
                in: .rect(cornerRadius: cornerRadius)
            )
        } else {
            background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                        .stroke(.white.opacity(0.10), lineWidth: 1)
                }
        }
    }

    @ViewBuilder
    func neProtoGlassCircle(tint: Color?, interactive: Bool = false) -> some View {
        if #available(iOS 26.0, *) {
            glassEffect(.regular.tint(tint).interactive(interactive), in: Circle())
        } else {
            background(.ultraThinMaterial, in: Circle())
                .overlay {
                    Circle()
                        .stroke(.white.opacity(0.14), lineWidth: 1)
                }
        }
    }
}
