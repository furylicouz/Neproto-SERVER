public enum PacketTunnelBundleIdentifier {
    public static func derive(from appBundleIdentifier: String?) -> String? {
        guard let appBundleIdentifier, !appBundleIdentifier.isEmpty else {
            return nil
        }
        return "\(appBundleIdentifier).PacketTunnel"
    }
}
