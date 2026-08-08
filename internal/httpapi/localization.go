package httpapi

import (
	"sort"
	"strconv"
	"strings"

	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

// parseAcceptLanguage returns the language tags of an Accept-Language header
// ordered by descending q-value. Wildcards are ignored and malformed entries
// skipped. Example: "zh-CN, zh;q=0.9, en;q=0.8" -> [zh-CN zh en].
func parseAcceptLanguage(header string) []string {
	type entry struct {
		tag string
		q   float64
	}
	var entries []entry
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					q = parsed
				}
			}
		}
		entries = append(entries, entry{tag: tag, q: q})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].q > entries[j].q })
	tags := make([]string, 0, len(entries))
	for _, e := range entries {
		tags = append(tags, e.tag)
	}
	return tags
}

// localizedEventInfo returns a copy of the event context whose title and
// description honor the Accept-Language header: the best matching translation
// wins, and a missing or blank translation falls back to the default text.
func localizedEventInfo(info v2query.MarketEventInfo, acceptLanguage string) v2query.MarketEventInfo {
	if len(info.Translations) == 0 {
		return info
	}
	for _, tag := range parseAcceptLanguage(acceptLanguage) {
		translation, ok := matchEventTranslation(info.Translations, tag)
		if !ok {
			continue
		}
		info.Title = fallbackText(translation.Title, info.Title)
		info.Description = fallbackText(translation.Description, info.Description)
		return info
	}
	return info
}

// matchEventTranslation finds the best translation for one language tag:
// an exact key wins, then a more specific key with the tag as prefix
// (zh matches zh-CN), then a shorter key that prefixes the tag (zh-CN
// falls back to zh).
func matchEventTranslation(translations map[string]types.EventTranslation, tag string) (types.EventTranslation, bool) {
	if translation, ok := translations[tag]; ok {
		return translation, true
	}
	for key, translation := range translations {
		if strings.HasPrefix(key, tag+"-") {
			return translation, true
		}
	}
	for key, translation := range translations {
		if strings.HasPrefix(tag, key+"-") {
			return translation, true
		}
	}
	return types.EventTranslation{}, false
}

func fallbackText(translated, fallback string) string {
	if strings.TrimSpace(translated) == "" {
		return fallback
	}
	return translated
}
