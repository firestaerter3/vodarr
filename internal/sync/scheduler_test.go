package sync

import (
	"regexp"
	"testing"
)

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
