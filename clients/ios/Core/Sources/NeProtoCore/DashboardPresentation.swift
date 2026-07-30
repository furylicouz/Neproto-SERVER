import Foundation

public struct FormattedTrafficRate: Equatable, Sendable {
    public let value: String
    public let unit: String

    public init(value: String, unit: String) {
        self.value = value
        self.unit = unit
    }
}

public enum DashboardPresentation {
    public static func rate(bytesPerSecond: Int64) -> FormattedTrafficRate {
        let bytes = Double(max(0, bytesPerSecond))
        let divisor: Double
        let unit: String

        switch bytes {
        case 1_000_000...:
            divisor = 1_000_000
            unit = "МБ/с"
        case 1_000...:
            divisor = 1_000
            unit = "КБ/с"
        default:
            divisor = 1
            unit = "Б/с"
        }

        let scaled = bytes / divisor
        let value: String
        if scaled >= 100 || divisor == 1 {
            value = String(format: "%.0f", scaled)
        } else {
            value = String(format: "%.1f", scaled)
                .replacingOccurrences(of: ".", with: ",")
        }
        return FormattedTrafficRate(value: value, unit: unit)
    }

    public static func duration(seconds: Int) -> String {
        let safeSeconds = max(0, seconds)
        return String(
            format: "%02d:%02d:%02d",
            safeSeconds / 3_600,
            (safeSeconds % 3_600) / 60,
            safeSeconds % 60
        )
    }
}
