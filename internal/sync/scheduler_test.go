package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/tmdb"
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
		name     string
		input    string
		patterns []*regexp.Regexp
		want     string
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
		{
			name:  "NL gesproken marker stripped to reveal year",
			input: "┃NL┃ Some Movie - 2022 [NL Gesproken]",
			want:  "2022",
		},
		{
			name:     "user pattern stripped, year preserved",
			input:    "┃NL┃ Director's Cut - 2015",
			patterns: []*regexp.Regexp{regexp.MustCompile(`(?i)director'?s cut`)},
			want:     "2015",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractNameYear(tc.input, tc.patterns)
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

func TestEnrichYearConflict(t *testing.T) {
	// Provider gives TMDBId=620 (Ghostbusters 1984) for a stream named
	// "Ghostbusters - 2016 [DOLBY]".  After fetching details for 620 and
	// finding year=1984 ≠ name year=2016, enrich must clear the provider ID
	// and retry title search, landing on TMDBId=999 (the mock 2016 film).

	mux := http.NewServeMux()

	// GET /movie/620 — the wrong movie (1984)
	mux.HandleFunc("/movie/620", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"title":        "Ghostbusters",
			"runtime":      105,
			"release_date": "1984-06-08",
		})
	})

	// GET /movie/620/external_ids — wrong IMDB ID
	mux.HandleFunc("/movie/620/external_ids", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"imdb_id": "tt0087332",
		})
	})

	// GET /search/movie — title retry with year=2016
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 999, "title": "Ghostbusters: Answer the Call", "release_date": "2016-07-15"},
			},
		})
	})

	// GET /movie/999/external_ids — correct IMDB ID
	mux.HandleFunc("/movie/999/external_ids", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"imdb_id": "tt1289401",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tc := tmdb.NewClient("testkey")
	tc.SetBaseURL(srv.URL)

	cfg := &config.Config{}
	cfg.TMDB.APIKey = "testkey"
	cfg.Sync.Parallelism = 1
	sched := &Scheduler{cfg: cfg, tmdb: tc}

	item := &index.Item{
		Type:     index.TypeMovie,
		XtreamID: 1,
		Name:     "┃NL┃ Ghostbusters - 2016 [DOLBY]",
		TMDBId:   "620",
	}

	enriched, err := sched.enrich(context.Background(), []*index.Item{item}, nil)
	if err != nil {
		t.Fatalf("enrich returned error: %v", err)
	}
	got := enriched[0]

	if got.TMDBId != "999" {
		t.Errorf("TMDBId = %q, want 999", got.TMDBId)
	}
	if got.IMDBId != "tt1289401" {
		t.Errorf("IMDBId = %q, want tt1289401", got.IMDBId)
	}
	if got.CanonicalName != "Ghostbusters: Answer the Call" {
		t.Errorf("CanonicalName = %q, want Ghostbusters: Answer the Call", got.CanonicalName)
	}

	t.Run("year matches -- no conflict", func(t *testing.T) {
		// provider TMDBId year (1984) matches name year (1984) -> no retry
		mux2 := http.NewServeMux()
		mux2.HandleFunc("/movie/620", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"title": "Ghostbusters", "runtime": 105, "release_date": "1984-06-08",
			})
		})
		mux2.HandleFunc("/movie/620/external_ids", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"imdb_id": "tt0087332"})
		})
		srv2 := httptest.NewServer(mux2)
		defer srv2.Close()

		tc2 := tmdb.NewClient("testkey")
		tc2.SetBaseURL(srv2.URL)
		cfg2 := &config.Config{}
		cfg2.TMDB.APIKey = "testkey"
		cfg2.Sync.Parallelism = 1
		sched2 := &Scheduler{cfg: cfg2, tmdb: tc2}

		item2 := &index.Item{
			Type: index.TypeMovie, XtreamID: 2,
			Name:   "┃NL┃ Ghostbusters - 1984 [DOLBY]", // name year matches TMDB year
			TMDBId: "620",
		}
		enriched2, _ := sched2.enrich(context.Background(), []*index.Item{item2}, nil)
		got2 := enriched2[0]
		if got2.TMDBId != "620" {
			t.Errorf("TMDBId = %q, want 620 (no conflict, should not retry)", got2.TMDBId)
		}
		if got2.IMDBId != "tt0087332" {
			t.Errorf("IMDBId = %q, want tt0087332", got2.IMDBId)
		}
	})

	t.Run("conflict but title search finds nothing -- provider ID restored", func(t *testing.T) {
		mux3 := http.NewServeMux()
		mux3.HandleFunc("/movie/620", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"title": "Ghostbusters", "runtime": 105, "release_date": "1984-06-08",
			})
		})
		mux3.HandleFunc("/movie/620/external_ids", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"imdb_id": "tt0087332"})
		})
		mux3.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
			// return empty results -- no match found for the year-constrained search
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
		})
		srv3 := httptest.NewServer(mux3)
		defer srv3.Close()

		tc3 := tmdb.NewClient("testkey")
		tc3.SetBaseURL(srv3.URL)
		cfg3 := &config.Config{}
		cfg3.TMDB.APIKey = "testkey"
		cfg3.Sync.Parallelism = 1
		sched3 := &Scheduler{cfg: cfg3, tmdb: tc3}

		item3 := &index.Item{
			Type: index.TypeMovie, XtreamID: 3,
			Name:   "┃NL┃ Ghostbusters - 2016 [DOLBY]", // conflict: name=2016, TMDB=1984
			TMDBId: "620",
		}
		enriched3, _ := sched3.enrich(context.Background(), []*index.Item{item3}, nil)
		got3 := enriched3[0]
		// provider ID should be restored so the item doesn't look brand-new next sync
		if got3.TMDBId != "620" {
			t.Errorf("TMDBId = %q, want 620 (restored after failed retry)", got3.TMDBId)
		}
	})
}
