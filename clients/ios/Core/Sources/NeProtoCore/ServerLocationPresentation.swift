import Foundation

public enum ServerLocationPresentation {
    public static func flag(forRegion region: String?, fallbackCountryCode: String? = nil) -> String? {
        guard let code = countryCode(forRegion: region) ?? canonicalCountryCode(fallbackCountryCode) else {
            return nil
        }
        let scalars = code.unicodeScalars.compactMap { scalar in
            UnicodeScalar(127_397 + scalar.value)
        }
        guard scalars.count == 2 else { return nil }
        return String(String.UnicodeScalarView(scalars))
    }

    public static func localizedCountryName(
        forRegion region: String?,
        fallbackCountryCode: String? = nil,
        locale: Locale = .current
    ) -> String? {
        guard let code = countryCode(forRegion: region) ?? canonicalCountryCode(fallbackCountryCode) else {
            return nil
        }
        return locale.localizedString(forRegionCode: code)
            ?? Locale(identifier: "en_US").localizedString(forRegionCode: code)
    }

    public static func title(forRegion region: String?, fallbackName: String, locale: Locale = .current) -> String {
        let name = fallbackName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let country = localizedCountryName(forRegion: region, locale: locale) else {
            return name
        }
        if normalized(country) == normalized(name) {
            return country
        }
        return "\(country) · \(name)"
    }

    private static func countryCode(forRegion region: String?) -> String? {
        guard let region else { return nil }
        let trimmed = region.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let components = trimmed.components(separatedBy: locationSeparators)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        for candidate in [trimmed] + components {
            if let direct = canonicalCountryCode(candidate) {
                return direct
            }
            let key = normalized(candidate)
            if let alias = localityAliases[key] {
                return alias
            }
            if let localized = localizedCountryCodes[key] {
                return localized
            }
        }
        return nil
    }

    private static func canonicalCountryCode(_ value: String?) -> String? {
        guard let value else { return nil }
        let code = value.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        guard code.utf8.count == 2,
              code.utf8.allSatisfy({ $0 >= Character("A").asciiValue! && $0 <= Character("Z").asciiValue! }),
              isoCountryCodes.contains(code) else {
            return nil
        }
        return code
    }

    private static func normalized(_ value: String) -> String {
        value.folding(
            options: [.caseInsensitive, .diacriticInsensitive],
            locale: Locale(identifier: "en_US_POSIX")
        ).lowercased()
    }

    private static let localityAliases: [String: String] = [
        "moscow": "RU", "москва": "RU",
        "amsterdam": "NL", "амстердам": "NL",
        "helsinki": "FI", "хельсинки": "FI",
        "frankfurt": "DE", "франкфурт": "DE",
        "london": "GB", "лондон": "GB",
        "paris": "FR", "париж": "FR",
        "stockholm": "SE", "стокгольм": "SE"
    ]

    private static let locationSeparators = CharacterSet(charactersIn: "·/,;|-")

    private static let localizedCountryCodes: [String: String] = {
        let locales = [Locale(identifier: "en_US"), Locale(identifier: "ru_RU")]
        var result: [String: String] = [:]
        for code in isoCountryCodes.sorted() {
            for locale in locales {
                if let name = locale.localizedString(forRegionCode: code) {
                    result[normalized(name)] = code
                }
            }
        }
        return result
    }()

    private static let isoCountryCodes = Set("""
        AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ
        CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR
        GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO
        JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR
        MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO
        RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV
        TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW
        """.split(whereSeparator: { $0.isWhitespace }).map(String.init))
}
