import SwiftUI
import UIKit

struct DiagnosticsView: View {
    let lines: [String]
    let onRefresh: () -> Void

    var body: some View {
        ScrollView(showsIndicators: false) {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 12) {
                    Label("Диагностика Packet Tunnel", systemImage: "waveform.path.ecg")
                        .font(.headline)
                        .foregroundStyle(NeProtoTheme.primaryText)
                    Spacer()
                    Circle()
                        .fill(lines.isEmpty ? Color.secondary : NeProtoTheme.purpleLight)
                        .frame(width: 8, height: 8)
                        .accessibilityHidden(true)
                }

                if lines.isEmpty {
                    VStack(spacing: 12) {
                        Image(systemName: "doc.text.magnifyingglass")
                            .font(.system(size: 38, weight: .light))
                        Text("Журнал пока пуст")
                            .font(.headline)
                        Text("Подключитесь к VPN или запросите свежую диагностику.")
                            .font(.subheadline)
                            .multilineTextAlignment(.center)
                    }
                    .foregroundStyle(NeProtoTheme.secondaryText)
                    .frame(maxWidth: .infinity, minHeight: 240)
                } else {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        ForEach(Array(lines.suffix(50).enumerated()), id: \.offset) { _, line in
                            Text(line)
                                .font(.caption.monospaced())
                                .foregroundStyle(NeProtoTheme.primaryText.opacity(0.88))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .textSelection(.enabled)
                        }
                    }
                }

                HStack(spacing: 12) {
                    Button(action: onRefresh) {
                        Label("Обновить", systemImage: "arrow.clockwise")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(NeProtoTheme.purple)

                    Button {
                        UIPasteboard.general.string = lines.joined(separator: "\n")
                    } label: {
                        Label("Копировать", systemImage: "doc.on.doc")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .tint(NeProtoTheme.purpleLight)
                    .disabled(lines.isEmpty)
                }
            }
            .padding(18)
            .background(NeProtoTheme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .padding(.vertical, 16)
        }
    }
}
