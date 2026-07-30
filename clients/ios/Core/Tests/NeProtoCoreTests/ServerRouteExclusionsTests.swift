import Testing
@testable import NeProtoCore

@Suite("NP/2 server route exclusions")
struct ServerRouteExclusionsTests {
    @Test("numeric server routes are classified and deduplicated")
    func parsesRoutes() throws {
        let routes = try ServerRouteExclusions(
            "2001:4860:4860::8888,104.171.136.10,104.171.136.10"
        )
        #expect(routes.ipv4 == ["104.171.136.10"])
        #expect(routes.ipv6 == ["2001:4860:4860::8888"])
    }

    @Test("empty and nonnumeric routes are rejected", arguments: ["", "vpn.example.com", "1.1.1.1,bad"])
    func rejectsInvalidRoutes(_ value: String) {
        #expect(throws: ServerRouteExclusionsError.self) {
            try ServerRouteExclusions(value)
        }
    }
}
