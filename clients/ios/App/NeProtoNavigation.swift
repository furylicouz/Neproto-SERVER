import SwiftUI

enum NeProtoSection: String, CaseIterable, Identifiable {
    case home
    case profiles
    case diagnostics

    var id: String { rawValue }

    var title: String {
        switch self {
        case .home: "Главная"
        case .profiles: "Профили"
        case .diagnostics: "Диагностика"
        }
    }

    var systemImage: String {
        switch self {
        case .home: "house.fill"
        case .profiles: "server.rack"
        case .diagnostics: "waveform.path.ecg"
        }
    }
}

struct AddServerMenu: View {
    let onAddServer: () -> Void
    let onScanQR: () -> Void

    var body: some View {
        Menu {
            Button(action: onScanQR) {
                Label("Сканировать QR", systemImage: "qrcode.viewfinder")
            }
            Button(action: onAddServer) {
                Label("Ввести вручную", systemImage: "keyboard")
            }
        } label: {
            Label("Добавить сервер", systemImage: "plus")
                .labelStyle(.iconOnly)
        }
        .accessibilityIdentifier("add-server")
    }
}
