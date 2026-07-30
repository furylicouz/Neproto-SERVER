import Foundation
import Network

public enum ServerRouteExclusionsError: Error, LocalizedError {
    case invalidAddresses

    public var errorDescription: String? {
        "NP/2 не вернул безопасный числовой адрес сервера для исключения маршрута."
    }
}

public struct ServerRouteExclusions: Equatable, Sendable {
    public let ipv4: [String]
    public let ipv6: [String]

    public init(_ commaSeparatedAddresses: String) throws {
        var ipv4 = Set<String>()
        var ipv6 = Set<String>()
        for rawAddress in commaSeparatedAddresses.split(separator: ",", omittingEmptySubsequences: true) {
            let address = String(rawAddress)
            if let parsed = IPv4Address(address) {
                ipv4.insert(parsed.debugDescription)
            } else if let parsed = IPv6Address(address) {
                ipv6.insert(parsed.debugDescription)
            } else {
                throw ServerRouteExclusionsError.invalidAddresses
            }
        }
        guard !ipv4.isEmpty || !ipv6.isEmpty else {
            throw ServerRouteExclusionsError.invalidAddresses
        }
        self.ipv4 = ipv4.sorted()
        self.ipv6 = ipv6.sorted()
    }
}
