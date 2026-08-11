import NeProtoCore
import SwiftUI

struct ProfileEditorView: View {
    let onSave: (ServerProfile, String) throws -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var serverIdentity = ""
    @State private var serverAddress = ""
    @State private var httpsPath = ""
    @State private var webRTCPath = ""
    @State private var http3Path = ""
    @State private var requireDatagrams = false
    @State private var secret = ""
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Сервер") {
                    TextField("Название", text: $name)
                    TextField("Домен", text: $serverIdentity)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                    TextField("IP сервера", text: $serverAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.numbersAndPunctuation)
                }
                Section {
                    TextField("HTTPS-путь", text: $httpsPath)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("WebRTC-путь", text: $webRTCPath)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("HTTP/3-путь (необязательно)", text: $http3Path)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Toggle("Требовать быстрый UDP", isOn: $requireDatagrams)
                        .disabled(http3Path.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                } header: {
                    Text("Приватные маршруты")
                } footer: {
                    Text("Скопируйте пути из клиентской конфигурации этого сервера. Они должны начинаться с / и отличаться друг от друга.")
                }
                Section("Ключ") {
                    SecureField("256-битный PSK", text: $secret)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Text("Ключ сохраняется только в Keychain этого устройства.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let errorMessage {
                    Section {
                        Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }
            }
            .navigationTitle("Новый сервер")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Сохранить", action: save)
                        .accessibilityIdentifier("save-server")
                }
            }
        }
    }

    private func save() {
        let profile = ServerProfile(
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            serverIdentity: serverIdentity.trimmingCharacters(in: .whitespacesAndNewlines),
            serverAddress: serverAddress.trimmingCharacters(in: .whitespacesAndNewlines),
            httpsPath: httpsPath.trimmingCharacters(in: .whitespacesAndNewlines),
            webRTCPath: webRTCPath.trimmingCharacters(in: .whitespacesAndNewlines),
            http3Path: http3Path.trimmingCharacters(in: .whitespacesAndNewlines),
            requireDatagrams: requireDatagrams,
            coverProfile: .web
        )
        do {
            try onSave(profile, secret.trimmingCharacters(in: .whitespacesAndNewlines))
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
