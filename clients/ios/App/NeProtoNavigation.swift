import SwiftUI

enum NeProtoSection: String, CaseIterable, Identifiable {
    case home
    case routes
    case diagnostics

    var id: String { rawValue }

    var title: String {
        switch self {
        case .home: "Главная"
        case .routes: "Маршруты"
        case .diagnostics: "Журнал"
        }
    }

    var systemImage: String {
        switch self {
        case .home: "house.fill"
        case .routes: "point.topleft.down.to.point.bottomright.curvepath"
        case .diagnostics: "person.crop.circle.fill"
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
