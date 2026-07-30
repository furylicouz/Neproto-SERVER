import Foundation
import Testing
@testable import NeProtoCore

@Suite("Server location presentation")
struct ServerLocationPresentationTests {
    @Test("ISO country code produces the correct flag and localized country")
    func isoCountryCode() {
        let locale = Locale(identifier: "ru_RU")

        #expect(ServerLocationPresentation.flag(forRegion: "NL") == "🇳🇱")
        #expect(ServerLocationPresentation.localizedCountryName(forRegion: "NL", locale: locale) == "Нидерланды")
        #expect(ServerLocationPresentation.title(forRegion: "NL", fallbackName: "n2-NL", locale: locale) == "Нидерланды · n2-NL")
    }

    @Test("localized and English country names resolve without a hardcoded server domain")
    func countryNames() {
        #expect(ServerLocationPresentation.flag(forRegion: "Netherlands") == "🇳🇱")
        #expect(ServerLocationPresentation.flag(forRegion: "Нидерланды") == "🇳🇱")
        #expect(ServerLocationPresentation.flag(forRegion: "Russia") == "🇷🇺")
    }

    @Test("compound country and city locations resolve to the country flag")
    func compoundLocations() {
        #expect(ServerLocationPresentation.flag(forRegion: "Россия · Москва") == "🇷🇺")
        #expect(ServerLocationPresentation.flag(forRegion: "NL/Amsterdam") == "🇳🇱")
        #expect(ServerLocationPresentation.flag(forRegion: "Netherlands, Amsterdam") == "🇳🇱")
    }

    @Test("unknown regions keep the safe globe fallback in the UI")
    func unknownRegion() {
        #expect(ServerLocationPresentation.flag(forRegion: "Primary") == nil)
        #expect(ServerLocationPresentation.localizedCountryName(forRegion: nil) == nil)
    }
}
