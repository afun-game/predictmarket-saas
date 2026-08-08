package event

import "strings"

// Market category catalog shown to end users. Values are stable lowercase
// keys; the admin console and hosted UI translate them into labels.
const (
	CategoryHot        = "hot"
	CategoryFootball   = "football"
	CategoryBasketball = "basketball"
	CategoryBaseball   = "baseball"
	CategoryBoxing     = "boxing"
	CategoryWeather    = "weather"
	CategoryBitcoin    = "bitcoin"
	CategoryOther      = "other"
)

// CatalogCategories returns the user-facing category keys in display order.
func CatalogCategories() []string {
	return []string{
		CategoryHot,
		CategoryFootball,
		CategoryBasketball,
		CategoryBaseball,
		CategoryBoxing,
		CategoryWeather,
		CategoryBitcoin,
		CategoryOther,
	}
}

// IsCatalogCategory reports whether the value is one of the catalog keys.
func IsCatalogCategory(category string) bool {
	switch category {
	case CategoryHot, CategoryFootball, CategoryBasketball, CategoryBaseball,
		CategoryBoxing, CategoryWeather, CategoryBitcoin, CategoryOther:
		return true
	}
	return false
}

// NormalizeCategory maps legacy/free-form source categories onto the catalog
// (e.g. "crypto"/"ethereum" -> "bitcoin", anything unknown -> "other").
func NormalizeCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case CategoryHot, CategoryFootball, CategoryBasketball, CategoryBaseball,
		CategoryBoxing, CategoryWeather, CategoryBitcoin, CategoryOther:
		return strings.ToLower(strings.TrimSpace(category))
	case "crypto", "ethereum":
		return CategoryBitcoin
	default:
		return CategoryOther
	}
}

// CategoryForLeague maps a sports league key onto the catalog; leagues
// without a dedicated catalog entry fall back to "other".
func CategoryForLeague(league string) string {
	switch strings.ToLower(strings.TrimSpace(league)) {
	case "nba", "wnba":
		return CategoryBasketball
	case "mlb", "lmb":
		return CategoryBaseball
	case "epl":
		return CategoryFootball
	case "boxing":
		return CategoryBoxing
	default:
		return CategoryOther
	}
}
