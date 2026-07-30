package main

import (
	"strings"
	"unicode"

	"neproto.local/chameleon/internal/cluster"
)

type tuiMapLocation struct {
	code      string
	name      string
	latitude  float64
	longitude float64
	priority  int
	aliases   []string
}

// Country anchors are deliberately kept offline. They provide stable labels
// and node placement without leaking server or client addresses to a GeoIP API.
var tuiMapCountries = []tuiMapLocation{
	{code: "US", name: "United States", latitude: 39.8, longitude: -98.6, priority: 1, aliases: []string{"usa", "united states", "united states of america", "сша"}},
	{code: "CA", name: "Canada", latitude: 56.1, longitude: -106.3, priority: 2, aliases: []string{"canada", "канада"}},
	{code: "MX", name: "Mexico", latitude: 23.6, longitude: -102.5, priority: 3, aliases: []string{"mexico", "мексика"}},
	{code: "BR", name: "Brazil", latitude: -10.8, longitude: -52.9, priority: 1, aliases: []string{"brazil", "brasil", "бразилия"}},
	{code: "AR", name: "Argentina", latitude: -38.4, longitude: -63.6, priority: 3, aliases: []string{"argentina", "аргентина"}},
	{code: "CL", name: "Chile", latitude: -33.4, longitude: -70.7, priority: 3, aliases: []string{"chile", "чили"}},
	{code: "GB", name: "United Kingdom", latitude: 54.7, longitude: -3.4, priority: 2, aliases: []string{"uk", "united kingdom", "great britain", "britain", "великобритания", "англия"}},
	{code: "IE", name: "Ireland", latitude: 53.2, longitude: -8.2, priority: 4, aliases: []string{"ireland", "ирландия"}},
	{code: "PT", name: "Portugal", latitude: 39.6, longitude: -8.0, priority: 4, aliases: []string{"portugal", "португалия"}},
	{code: "ES", name: "Spain", latitude: 40.2, longitude: -3.7, priority: 3, aliases: []string{"spain", "испания"}},
	{code: "FR", name: "France", latitude: 46.2, longitude: 2.2, priority: 2, aliases: []string{"france", "франция"}},
	{code: "BE", name: "Belgium", latitude: 50.8, longitude: 4.5, priority: 4, aliases: []string{"belgium", "бельгия"}},
	{code: "NL", name: "Netherlands", latitude: 52.1, longitude: 5.3, priority: 2, aliases: []string{"netherlands", "holland", "нидерланды", "голландия"}},
	{code: "DE", name: "Germany", latitude: 51.2, longitude: 10.4, priority: 2, aliases: []string{"germany", "deutschland", "германия"}},
	{code: "CH", name: "Switzerland", latitude: 46.8, longitude: 8.2, priority: 4, aliases: []string{"switzerland", "швейцария"}},
	{code: "IT", name: "Italy", latitude: 42.8, longitude: 12.5, priority: 3, aliases: []string{"italy", "италия"}},
	{code: "AT", name: "Austria", latitude: 47.5, longitude: 14.6, priority: 4, aliases: []string{"austria", "австрия"}},
	{code: "CZ", name: "Czechia", latitude: 49.8, longitude: 15.5, priority: 4, aliases: []string{"czechia", "czech republic", "чехия"}},
	{code: "PL", name: "Poland", latitude: 51.9, longitude: 19.1, priority: 3, aliases: []string{"poland", "польша"}},
	{code: "DK", name: "Denmark", latitude: 56.2, longitude: 9.5, priority: 4, aliases: []string{"denmark", "дания"}},
	{code: "NO", name: "Norway", latitude: 61.0, longitude: 8.5, priority: 3, aliases: []string{"norway", "норвегия"}},
	{code: "SE", name: "Sweden", latitude: 62.0, longitude: 15.0, priority: 3, aliases: []string{"sweden", "швеция"}},
	{code: "FI", name: "Finland", latitude: 64.0, longitude: 26.0, priority: 3, aliases: []string{"finland", "финляндия"}},
	{code: "UA", name: "Ukraine", latitude: 49.0, longitude: 31.4, priority: 3, aliases: []string{"ukraine", "украина"}},
	{code: "RU", name: "Russia", latitude: 61.5, longitude: 90.0, priority: 1, aliases: []string{"russia", "russian federation", "россия", "рф"}},
	{code: "TR", name: "Turkey", latitude: 39.0, longitude: 35.2, priority: 3, aliases: []string{"turkey", "turkiye", "турция"}},
	{code: "GE", name: "Georgia", latitude: 42.3, longitude: 43.4, priority: 4, aliases: []string{"georgia", "грузия"}},
	{code: "KZ", name: "Kazakhstan", latitude: 48.0, longitude: 68.0, priority: 3, aliases: []string{"kazakhstan", "казахстан"}},
	{code: "IL", name: "Israel", latitude: 31.0, longitude: 34.8, priority: 4, aliases: []string{"israel", "израиль"}},
	{code: "AE", name: "United Arab Emirates", latitude: 24.3, longitude: 54.4, priority: 3, aliases: []string{"uae", "united arab emirates", "emirates", "оаэ"}},
	{code: "SA", name: "Saudi Arabia", latitude: 24.0, longitude: 45.0, priority: 4, aliases: []string{"saudi arabia", "саудовская аравия"}},
	{code: "EG", name: "Egypt", latitude: 26.8, longitude: 30.8, priority: 3, aliases: []string{"egypt", "египет"}},
	{code: "NG", name: "Nigeria", latitude: 9.1, longitude: 8.7, priority: 4, aliases: []string{"nigeria", "нигерия"}},
	{code: "ZA", name: "South Africa", latitude: -30.6, longitude: 22.9, priority: 2, aliases: []string{"south africa", "rsa", "юар", "южная африка"}},
	{code: "IN", name: "India", latitude: 22.6, longitude: 79.0, priority: 1, aliases: []string{"india", "индия"}},
	{code: "PK", name: "Pakistan", latitude: 30.4, longitude: 69.3, priority: 4, aliases: []string{"pakistan", "пакистан"}},
	{code: "CN", name: "China", latitude: 35.9, longitude: 104.2, priority: 1, aliases: []string{"china", "prc", "китай"}},
	{code: "JP", name: "Japan", latitude: 36.2, longitude: 138.3, priority: 2, aliases: []string{"japan", "япония"}},
	{code: "KR", name: "South Korea", latitude: 36.3, longitude: 128.0, priority: 3, aliases: []string{"south korea", "korea", "корея", "южная корея"}},
	{code: "SG", name: "Singapore", latitude: 1.35, longitude: 103.8, priority: 3, aliases: []string{"singapore", "сингапур"}},
	{code: "TH", name: "Thailand", latitude: 15.9, longitude: 101.0, priority: 4, aliases: []string{"thailand", "таиланд"}},
	{code: "VN", name: "Vietnam", latitude: 16.0, longitude: 107.8, priority: 4, aliases: []string{"vietnam", "вьетнам"}},
	{code: "ID", name: "Indonesia", latitude: -2.5, longitude: 118.0, priority: 3, aliases: []string{"indonesia", "индонезия"}},
	{code: "PH", name: "Philippines", latitude: 12.9, longitude: 122.7, priority: 4, aliases: []string{"philippines", "филиппины"}},
	{code: "AU", name: "Australia", latitude: -25.3, longitude: 133.8, priority: 1, aliases: []string{"australia", "австралия"}},
	{code: "NZ", name: "New Zealand", latitude: -41.0, longitude: 174.0, priority: 3, aliases: []string{"new zealand", "новая зеландия"}},
}

var tuiMapCities = []tuiMapLocation{
	{code: "RU", name: "Moscow", latitude: 55.7558, longitude: 37.6173, aliases: []string{"moscow", "москва", "msk"}},
	{code: "NL", name: "Amsterdam", latitude: 52.3676, longitude: 4.9041, aliases: []string{"amsterdam", "амстердам"}},
	{code: "FI", name: "Helsinki", latitude: 60.1699, longitude: 24.9384, aliases: []string{"helsinki", "хельсинки"}},
	{code: "DE", name: "Frankfurt", latitude: 50.1109, longitude: 8.6821, aliases: []string{"frankfurt", "frankfurt am main", "франкфурт"}},
	{code: "GB", name: "London", latitude: 51.5072, longitude: -0.1276, aliases: []string{"london", "лондон"}},
	{code: "FR", name: "Paris", latitude: 48.8566, longitude: 2.3522, aliases: []string{"paris", "париж"}},
	{code: "SE", name: "Stockholm", latitude: 59.3293, longitude: 18.0686, aliases: []string{"stockholm", "стокгольм"}},
	{code: "PL", name: "Warsaw", latitude: 52.2297, longitude: 21.0122, aliases: []string{"warsaw", "варшава"}},
	{code: "US", name: "New York", latitude: 40.7128, longitude: -74.0060, aliases: []string{"new york", "nyc", "нью йорк"}},
	{code: "US", name: "Los Angeles", latitude: 34.0522, longitude: -118.2437, aliases: []string{"los angeles", "ла"}},
	{code: "JP", name: "Tokyo", latitude: 35.6762, longitude: 139.6503, aliases: []string{"tokyo", "токио"}},
	{code: "SG", name: "Singapore", latitude: 1.3521, longitude: 103.8198, aliases: []string{"singapore", "сингапур"}},
}

func locateTUIClusterNode(node cluster.Node) (tuiMapLocation, bool) {
	candidates := []string{node.Region, node.Name, node.ID}
	for _, location := range tuiMapCities {
		if tuiLocationMatches(location, candidates) {
			return location, true
		}
	}
	for _, location := range tuiMapCountries {
		if tuiLocationMatches(location, candidates) {
			return location, true
		}
	}
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(node.PublicIdentity, ".")), ".")
	if len(labels) > 1 {
		if location, ok := tuiMapCountryByCode(labels[len(labels)-1]); ok {
			return location, true
		}
	}
	return tuiMapLocation{}, false
}

func tuiLocationMatches(location tuiMapLocation, candidates []string) bool {
	wanted := normalizeTUILocation(location.code)
	aliases := append([]string{location.name}, location.aliases...)
	for _, candidate := range candidates {
		normalized := normalizeTUILocation(candidate)
		if normalized == "" {
			continue
		}
		for _, token := range strings.Fields(normalized) {
			if token == wanted {
				return true
			}
		}
		for _, alias := range aliases {
			alias = normalizeTUILocation(alias)
			if normalized == alias || (len([]rune(alias)) >= 4 && strings.Contains(normalized, alias)) {
				return true
			}
		}
	}
	return false
}

func tuiMapCountryByCode(code string) (tuiMapLocation, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, location := range tuiMapCountries {
		if location.code == code {
			return location, true
		}
	}
	return tuiMapLocation{}, false
}

func normalizeTUILocation(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}), " ")
}
