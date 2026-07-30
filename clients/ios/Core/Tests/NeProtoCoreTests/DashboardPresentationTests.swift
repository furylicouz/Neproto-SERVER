import Testing
@testable import NeProtoCore

@Suite("Dashboard presentation")
struct DashboardPresentationTests {
    @Test("traffic rate uses readable Russian units")
    func trafficRate() {
        #expect(DashboardPresentation.rate(bytesPerSecond: -1) == .init(value: "0", unit: "Б/с"))
        #expect(DashboardPresentation.rate(bytesPerSecond: 1_500) == .init(value: "1,5", unit: "КБ/с"))
        #expect(DashboardPresentation.rate(bytesPerSecond: 12_800_000) == .init(value: "12,8", unit: "МБ/с"))
    }

    @Test("connection duration is stable HH:MM:SS")
    func duration() {
        #expect(DashboardPresentation.duration(seconds: -10) == "00:00:00")
        #expect(DashboardPresentation.duration(seconds: 3_661) == "01:01:01")
    }
}
