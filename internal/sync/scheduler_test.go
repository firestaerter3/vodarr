package sync

import (
	"regexp"
	"testing"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"01:32:45", 5565.0},
		{"00:45:00", 2700.0},
		{"45:00", 2700.0},
		{"1:30", 90.0},
		{"90", 5400.0}, // bare minutes
		{"", 0},
		{"invalid", 0},
	}
	for _, c := range cases {
		got := parseDuration(c.input)
		if got != c.want {
			t.Errorf("parseDuration(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestCleanTitleForSearch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		patterns []string
		want     string
	}{
		{
			name:  "no prefix unchanged",
			input: "Scarface",
			want:  "Scarface",
		},
		{
			name:  "single NL prefix stripped",
			input: "| NL | Scarface",
			want:  "Scarface",
		},
		{
			name:  "single EN prefix stripped",
			input: "| EN | The Matrix",
			want:  "The Matrix",
		},
		{
			name:  "single DE prefix stripped",
			input: "| DE | Das Boot",
			want:  "Das Boot",
		},
		{
			name:  "multiple prefixes all stripped",
			input: "| NL | HD | Scarface",
			want:  "Scarface",
		},
		{
			name:  "three prefixes all stripped",
			input: "| NL | HD | 4K | Inception",
			want:  "Inception",
		},
		{
			name:  "prefix with extra whitespace stripped",
			input: "|  NL  |  The Matrix",
			want:  "The Matrix",
		},
		{
			name:     "user pattern stripped",
			input:    "Scarface (NL GESPROKEN)",
			patterns: []string{`\s*\(NL GESPROKEN\)`},
			want:     "Scarface",
		},
		{
			name:     "iptv prefix and user pattern both stripped",
			input:    "| NL | Scarface (NL GESPROKEN)",
			patterns: []string{`\s*\(NL GESPROKEN\)`},
			want:     "Scarface",
		},
		{
			name:     "multiple user patterns applied",
			input:    "Scarface [HD] (DUBBED)",
			patterns: []string{`\s*\[HD\]`, `\s*\(DUBBED\)`},
			want:     "Scarface",
		},
		{
			name:  "result trimmed of surrounding whitespace",
			input: "| NL |   The Matrix   ",
			want:  "The Matrix",
		},
		{
			name:  "unicode box-drawing pipe stripped",
			input: "┃NL┃ Scarface",
			want:  "Scarface",
		},
		{
			name:  "tab and unicode pipe stripped",
			input: "\t┃NL┃ Scarface",
			want:  "Scarface",
		},
		{
			name:  "multiple unicode pipes stripped",
			input: "┃NL┃ HD┃ Inception",
			want:  "Inception",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var compiled []*regexp.Regexp
			for _, p := range tc.patterns {
				compiled = append(compiled, regexp.MustCompile(p))
			}
			got := cleanTitleForSearch(tc.input, compiled)
			if got != tc.want {
				t.Errorf("cleanTitleForSearch(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractTrailingYear(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Vrouwenvleugel (1993)", "1993"},
		{"Some Movie (2021)", "2021"},
		{"No Year Here", ""},
		{"Bad Year (99)", ""},
		{"Trailing Space (2010) ", "2010"},
		{"┃NL┃ Movie (2005)", "2005"},
	}
	for _, c := range cases {
		got := extractTrailingYear(c.input)
		if got != c.want {
			t.Errorf("extractTrailingYear(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestCleanTitleStripsTrailingYear(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Vrouwenvleugel (1993)", "Vrouwenvleugel"},
		{"┃NL┃ Vrouwenvleugel (1993)", "Vrouwenvleugel"},
		{"Scarface", "Scarface"},
	}
	for _, c := range cases {
		got := cleanTitleForSearch(c.input, nil)
		if got != c.want {
			t.Errorf("cleanTitleForSearch(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractTrailingYearAllPatterns(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Parens (existing)
		{"Movie (1993)", "1993"},
		// Dash
		{"Movie - 2021", "2021"},
		{"Movie - 2005 ", "2005"},
		// Bracket
		{"Movie [2010]", "2010"},
		{"Movie [1999] ", "1999"},
		// No match
		{"No Year Here", ""},
		{"Bad Year (99)", ""},
	}
	for _, c := range cases {
		got := extractTrailingYear(c.input)
		if got != c.want {
			t.Errorf("extractTrailingYear(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractNameYear(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "dash year after quality marker",
			input: "┃NL┃ Ghostbusters - 2016 [DOLBY]",
			want:  "2016",
		},
		{
			name:  "dash year after 4K marker",
			input: "┃NL┃ Avengers: Endgame - 2019 4K",
			want:  "2019",
		},
		{
			name:  "dash year after HEVC marker",
			input: "┃NL┃ Inception - 2010 HEVC",
			want:  "2010",
		},
		{
			name:  "parens year",
			input: "┃NL┃ Some Movie (2021)",
			want:  "2021",
		},
		{
			name:  "bracket year",
			input: "┃NL┃ Some Movie [2021]",
			want:  "2021",
		},
		{
			name:  "year embedded in title (Blade Runner 2049) — no trailing pattern",
			input: "┃NL┃ Blade Runner 2049",
			want:  "",
		},
		{
			name:  "year embedded in title (1917) — no trailing pattern",
			input: "┃NL┃ 1917",
			want:  "",
		},
		{
			name:  "Fear Street 1994 with release year in parens — release year wins",
			input: "┃NL┃ Fear Street: 1994 (2021)",
			want:  "2021",
		},
		{
			name:  "no year at all",
			input: "┃NL┃ Scarface",
			want:  "",
		},
		{
			name:  "plain name no prefix",
			input: "The Matrix - 1999",
			want:  "1999",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractNameYear(tc.input, nil)
			if got != tc.want {
				t.Errorf("extractNameYear(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCleanTitleQualityMarkers(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// HEVC stripped
		{"The Matrix HEVC", "The Matrix"},
		{"The Matrix hevc", "The Matrix"},
		// 4K stripped
		{"Inception 4K", "Inception"},
		// DOLBY stripped (various forms)
		{"Movie [DOLBY]", "Movie"},
		{"Movie (DOLBY)", "Movie"},
		{"Movie DOLBY", "Movie"},
		// NL GESPROKEN stripped
		{"Movie (NL GESPROKEN)", "Movie"},
		{"Movie [NL Gesproken]", "Movie"},
		// Dash year stripped
		{"Movie - 2021", "Movie"},
		// Bracket year stripped
		{"Movie [2021]", "Movie"},
		// Marker after year: HEVC stripped first so year anchor works
		{"Movie - 2021 HEVC", "Movie"},
		{"Movie [2021] 4K", "Movie"},
		// Combined
		{"Movie HEVC 4K [DOLBY] - 2020", "Movie"},
		{"┃NL┃ Movie HEVC - 2021", "Movie"},
	}
	for _, c := range cases {
		got := cleanTitleForSearch(c.input, nil)
		if got != c.want {
			t.Errorf("cleanTitleForSearch(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
