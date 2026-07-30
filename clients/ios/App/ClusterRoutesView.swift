import NeProtoCore
import SwiftUI

struct ClusterRoutesView: View {
    @EnvironmentObject private var profileStore: ProfileStore

    let profile: ServerProfile?
    let onError: (Error) -> Void

    @State private var isAddingRoute = false

    var body: some View {
        Group {
            if let profile, let clusterID = profile.clusterID,
               let state = profileStore.clusterStates[clusterID] {
                routeList(profile: profile, state: state)
            } else {
                VStack(spacing: 14) {
                    Image(systemName: "point.topleft.down.to.point.bottomright.curvepath")
                        .font(.system(size: 46, weight: .light))
                        .foregroundStyle(NeProtoTheme.purpleLight)
                    Text("Нет маршрутов").font(.title2.weight(.semibold))
                    Text("Подключитесь к кластерному серверу NP/2, чтобы синхронизировать маршруты.")
                        .multilineTextAlignment(.center)
                        .foregroundStyle(.secondary)
                }
                .padding(24)
            }
        }
        .sheet(isPresented: $isAddingRoute) {
            if let profile {
                LocalRouteEditorView(
                    servers: availableServers(for: profile),
                    onSave: { route in
                        try profileStore.upsertLocalRoute(route, profileID: profile.id)
                    }
                )
            }
        }
    }

    private func routeList(profile: ServerProfile, state: ClusterClientState) -> some View {
        List {
            Section {
                LabeledContent("Ревизия", value: String(state.revision))
                LabeledContent("Синхронизация", value: state.synchronizedAt.formatted(date: .abbreviated, time: .shortened))
            } header: {
                Text("Кластер")
            }

            if state.adminRoutes.isEmpty {
                Section("Маршруты администратора") {
                    Text("Администратор не назначил маршруты этому пользователю.")
                        .foregroundStyle(.secondary)
                }
            } else {
                Section("Маршруты администратора") {
                    ForEach(state.adminRoutes) { route in
                        routeRow(route, locked: true, profile: profile)
                    }
                }
            }

            Section {
                if state.localRoutes.isEmpty {
                    Text(state.permissions.allowClientRoutes ? "Локальные маршруты не добавлены." : "Локальные маршруты запрещены администратором.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(state.localRoutes) { route in
                        routeRow(route, locked: false, profile: profile)
                    }
                    .onDelete { offsets in
                        for index in offsets {
                            do {
                                try profileStore.removeLocalRoute(routeID: state.localRoutes[index].id, profileID: profile.id)
                            } catch {
                                onError(error)
                            }
                        }
                    }
                }
            } header: {
                HStack {
                    Text("Мои маршруты")
                    Spacer()
                    if state.permissions.allowClientRoutes {
                        Button { isAddingRoute = true } label: { Image(systemName: "plus.circle.fill") }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Добавить маршрут")
                    }
                }
            }
        }
        .scrollContentBackground(.hidden)
    }

    private func routeRow(_ route: ClusterRoute, locked: Bool, profile: ServerProfile) -> some View {
        HStack(spacing: 12) {
            Image(systemName: locked ? "lock.shield.fill" : "arrow.triangle.branch")
                .foregroundStyle(locked ? .orange : NeProtoTheme.purpleLight)
            VStack(alignment: .leading, spacing: 3) {
                Text(route.name).font(.headline)
                Text(routeDescription(route, profile: profile))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer()
            if route.mandatory {
                Text("Обязательно")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.orange)
            }
        }
        .opacity(route.enabled ? 1 : 0.55)
    }

    private func routeDescription(_ route: ClusterRoute, profile: ServerProfile) -> String {
        let visibleDomains = route.match.domainSuffixes.filter {
            $0 != "np2-geodata-never-match.invalid"
        }
        let geoSites = route.match.geoSiteCategories.map { "GeoSite: \($0)" }
        let geoIPs = route.match.geoIPCountries.map { "GeoIP: \($0.uppercased())" }
        let targets = visibleDomains + route.match.cidrs + geoSites + geoIPs
        let target = targets.isEmpty ? "весь трафик" : targets.joined(separator: ", ")
        let action: String
        switch route.action.kind {
        case .direct: action = "напрямую"
        case .current: action = "через текущий сервер"
        case .block: action = "блокировать"
        case .auto: action = "автовыбор"
        case .node, .chain:
            let names = route.action.nodeIDs.map { nodeID in
                availableServers(for: profile).first(where: { $0.clusterNodeID == nodeID })?.name ?? nodeID
            }
            action = "через " + names.joined(separator: " → ")
        }
        return "\(target) • \(action)"
    }

    private func availableServers(for profile: ServerProfile) -> [ServerProfile] {
        profileStore.profiles.filter {
            $0.clusterID == profile.clusterID && $0.clusterAvailable && $0.clusterNodeID != nil
        }
    }
}

private struct LocalRouteEditorView: View {
    @Environment(\.dismiss) private var dismiss

    let servers: [ServerProfile]
    let onSave: (ClusterRoute) throws -> Void

    @State private var name = ""
    @State private var domains = ""
    @State private var cidrs = ""
    @State private var ports = ""
    @State private var action = ClusterRouteActionKind.auto
    @State private var selectedNodeID = ""
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Маршрут") {
                    TextField("Название", text: $name)
                    TextField("Домены: youtube.com, example.org", text: $domains)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("CIDR: 8.8.8.0/24", text: $cidrs)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Порты: 80,443,1000-2000", text: $ports)
                        .keyboardType(.numbersAndPunctuation)
                }
                Section("Действие") {
                    Picker("Направление", selection: $action) {
                        Text("Автовыбор").tag(ClusterRouteActionKind.auto)
                        Text("Текущий сервер").tag(ClusterRouteActionKind.current)
                        Text("Другой сервер").tag(ClusterRouteActionKind.node)
                        Text("С текущего сервера").tag(ClusterRouteActionKind.direct)
                        Text("Блокировать").tag(ClusterRouteActionKind.block)
                    }
                    if action == .node {
                        Picker("Сервер", selection: $selectedNodeID) {
                            ForEach(servers) { server in
                                Text(server.name).tag(server.clusterNodeID ?? "")
                            }
                        }
                    }
                }
                if let errorMessage {
                    Section { Text(errorMessage).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Новый маршрут")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Добавить", action: save)
                }
            }
            .onAppear {
                if selectedNodeID.isEmpty { selectedNodeID = servers.first?.clusterNodeID ?? "" }
            }
        }
    }

    private func save() {
        do {
            let nodeIDs = action == .node ? [selectedNodeID] : []
            let route = ClusterRoute(
                id: "local-\(UUID().uuidString.lowercased())",
                name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                priority: 100,
                enabled: true,
                source: .client,
                mandatory: false,
                match: ClusterRouteMatch(
                    domainSuffixes: tokens(domains).map { $0.lowercased() },
                    cidrs: tokens(cidrs),
                    portRanges: try parsePortRanges(ports),
                    protocols: []
                ),
                action: ClusterRouteAction(kind: action, nodeIDs: nodeIDs)
            )
            try onSave(route)
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func tokens(_ value: String) -> [String] {
        value.split(whereSeparator: { $0 == "," || $0 == ";" || $0.isWhitespace }).map(String.init)
    }

    private func parsePortRanges(_ value: String) throws -> [ClusterPortRange] {
        try tokens(value).map { token in
            let parts = token.split(separator: "-", omittingEmptySubsequences: false)
            guard let first = parts.first.flatMap({ UInt16($0) }), first > 0,
                  parts.count <= 2 else { throw ClusterRouteValidationError.invalidRoute }
            let last = parts.count == 2 ? UInt16(parts[1]) : first
            guard let last, last >= first else { throw ClusterRouteValidationError.invalidRoute }
            return ClusterPortRange(from: first, to: last)
        }
    }
}
