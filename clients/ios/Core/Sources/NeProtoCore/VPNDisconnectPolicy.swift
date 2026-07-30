public enum VPNDisconnectPolicy {
    public static func shouldReportError(wasUserInitiated: Bool) -> Bool {
        !wasUserInitiated
    }
}
