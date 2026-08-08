package event

import "testing"

func TestNormalizeCategory(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "hot", want: "hot"},
		{input: "football", want: "football"},
		{input: "basketball", want: "basketball"},
		{input: "baseball", want: "baseball"},
		{input: "boxing", want: "boxing"},
		{input: "weather", want: "weather"},
		{input: "bitcoin", want: "bitcoin"},
		{input: "other", want: "other"},
		{input: "crypto", want: "bitcoin"},
		{input: "ethereum", want: "bitcoin"},
		{input: "ETHEREUM", want: "bitcoin"},
		{input: "  Crypto  ", want: "bitcoin"},
		{input: "politics", want: "other"},
		{input: "esports", want: "other"},
		{input: "tennis", want: "other"},
		{input: "technology", want: "other"},
		{input: "", want: "other"},
	}
	for _, test := range tests {
		if got := NormalizeCategory(test.input); got != test.want {
			t.Errorf("NormalizeCategory(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestCategoryForLeague(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "nba", want: "basketball"},
		{input: "wnba", want: "basketball"},
		{input: "WNBA", want: "basketball"},
		{input: "mlb", want: "baseball"},
		{input: "lmb", want: "baseball"},
		{input: "epl", want: "football"},
		{input: "boxing", want: "boxing"},
		{input: "nfl", want: "other"},
		{input: "nhl", want: "other"},
		{input: "", want: "other"},
	}
	for _, test := range tests {
		if got := CategoryForLeague(test.input); got != test.want {
			t.Errorf("CategoryForLeague(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestCatalogCategories(t *testing.T) {
	got := CatalogCategories()
	if len(got) != 8 {
		t.Fatalf("CatalogCategories() length = %d, want 8", len(got))
	}
	for _, category := range got {
		if !IsCatalogCategory(category) {
			t.Errorf("CatalogCategories() contains non-catalog key %q", category)
		}
	}
}
