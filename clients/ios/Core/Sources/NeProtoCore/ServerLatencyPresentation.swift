import Foundation

public enum ServerLatencyPresentation {
    public static func bars(milliseconds: Int?) -> Int {
        guard let milliseconds, milliseconds >= 0 else { return 0 }
        return switch milliseconds {
        case ...80: 4
        case ...160: 3
        case ...300: 2
        default: 1
        }
    }

    public static func text(milliseconds: Int?) -> String {
        guard let milliseconds, milliseconds >= 0 else { return "—" }
        return "\(milliseconds) мс"
    }
}
