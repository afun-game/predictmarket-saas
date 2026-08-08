package httpapi

import (
	"reflect"
	"testing"

	"github.com/afun-game/predictmarket-saas/internal/v2query"
	"github.com/afun-game/predictmarket-saas/pkg/types"
)

func TestParseAcceptLanguage(t *testing.T) {
	tests := []struct {
		header string
		want   []string
	}{
		{header: "", want: []string{}},
		{header: "zh-CN, zh;q=0.9, en;q=0.8", want: []string{"zh-CN", "zh", "en"}},
		{header: "en;q=0.5, fr", want: []string{"fr", "en"}},
		{header: "*, zh", want: []string{"zh"}},
		{header: "en-US;q=0.7;foo=bar, de", want: []string{"de", "en-US"}},
		{header: "  ;q=0.1", want: []string{}},
	}
	for _, test := range tests {
		if got := parseAcceptLanguage(test.header); !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseAcceptLanguage(%q) = %v, want %v", test.header, got, test.want)
		}
	}
}

func TestLocalizedEventInfo(t *testing.T) {
	translations := map[string]types.EventTranslation{
		"zh-CN": {Title: "上海会下雨吗？", Description: "天气事件"},
		"en":    {Title: "Will it rain?"},
	}
	base := v2query.MarketEventInfo{Title: "Will it rain in Shanghai?", Description: "Weather event", Translations: translations}

	tests := []struct {
		name      string
		header    string
		wantTitle string
		wantDesc  string
	}{
		{name: "exact match", header: "zh-CN,zh;q=0.9", wantTitle: "上海会下雨吗？", wantDesc: "天气事件"},
		{name: "prefix match zh -> zh-CN", header: "zh", wantTitle: "上海会下雨吗？", wantDesc: "天气事件"},
		{name: "second choice", header: "fr,en;q=0.9", wantTitle: "Will it rain?", wantDesc: "Weather event"},
		{name: "no match falls back", header: "ja", wantTitle: "Will it rain in Shanghai?", wantDesc: "Weather event"},
		{name: "blank translated description falls back", header: "en", wantTitle: "Will it rain?", wantDesc: "Weather event"},
		{name: "empty header falls back", header: "", wantTitle: "Will it rain in Shanghai?", wantDesc: "Weather event"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := localizedEventInfo(base, test.header)
			if got.Title != test.wantTitle || got.Description != test.wantDesc {
				t.Errorf("localizedEventInfo(%q) = (%q, %q), want (%q, %q)",
					test.header, got.Title, got.Description, test.wantTitle, test.wantDesc)
			}
		})
	}
}

func TestLocalizedEventInfoNoTranslations(t *testing.T) {
	base := v2query.MarketEventInfo{Title: "Default", Description: "Desc"}
	got := localizedEventInfo(base, "zh-CN")
	if got.Title != "Default" || got.Description != "Desc" {
		t.Errorf("localizedEventInfo() without translations = (%q, %q)", got.Title, got.Description)
	}
}
