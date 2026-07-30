import Foundation

public enum VPNConfigurationError {
    private static let vpnErrorDomain = "NEVPNErrorDomain"
    private static let configurationErrorDomain = "NEConfigurationErrorDomain"
    private static let vpnConfigurationStaleCode = 4
    private static let preferencesConfigurationStaleCode = 5

    public static func isStale(_ error: Error) -> Bool {
        let error = error as NSError
        return (error.domain == vpnErrorDomain && error.code == vpnConfigurationStaleCode)
            || (error.domain == configurationErrorDomain && error.code == preferencesConfigurationStaleCode)
    }
}
