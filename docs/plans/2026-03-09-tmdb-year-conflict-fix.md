# TMDB Year-Conflict Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to execute this plan task-by-task.

**Goal:** When a provider-supplied TMDBId points to the wrong movie (e.g. the 1984 Ghostbusters ID on a stream named "Ghostbusters - 2016"), detect the year mismatch after fetching TMDB details and retry with the correct year.

**Architecture:** Add a helper `extractNameYear` that strips IPTV prefix + quality markers from a raw stream name and returns the trailing year. In `enrich`, after `GetMovieDetails` sets the TMDB release year, compare it against the name year. On mismatch, clear provider IDs and retry via `resolveByTitle` with the name year, then re-fetch external IDs.

**Tech Stack:** Go, existing regexes in `internal/sync/scheduler.go`, TMDB client in `internal/tmdb/client.go`

---

### Task 1: Add `extractNameYear` helper + tests

**Files:**
- Modify: `internal/sync/scheduler.go` (after `extractTrailingYear`, ~line 62)
- Modify: `internal/sync/scheduler_test.go` (after `TestCleanTitleForSearch`)

---

**Step 1: Write the failing test**

Add to `internal/sync/scheduler_test.go`, after the existing `TestCleanTitleForSearch` block:

```go
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
			input: "┃NL┃Aladdin - 2019 4K",
			want:  "2019",
		},
		{
			name:  "dash year after HEVC marker",
			input: "┃NL┃ Unstoppable - 2010 HEVC",
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
```

**Step 2: Run the test to verify it fails**

```bash
cd internal/sync && go test -run TestExtractNameYear -v
```

Expected: `FAIL` — `extractNameYear` undefined.

**Step 3: Add the implementation**

In `internal/sync/scheduler.go`, after the `extractTrailingYear` function (~line 62), add:

```go
// extractNameYear extracts a trailing 4-digit year from a raw Xtream stream
// name after stripping IPTV prefixes and quality/language markers (HEVC, 4K,
// DOLBY, NL GESPROKEN, user patterns).  Unlike cleanTitleForSearch it does NOT
// strip year patterns — it strips only the noise that follows the year, so
// that extractTrailingYear can find it at the end of the string.
// Returns "" if no trailing year pattern is present.
func extractNameYear(name string, patterns []*regexp.Regexp) string {
	title := iptvPrefixRe.ReplaceAllString(name, "")
	title = hevcRe.ReplaceAllString(title, "")
	title = fourKRe.ReplaceAllString(title, "")
	title = dolbyRe.ReplaceAllString(title, "")
	title = nlGespokenRe.ReplaceAllString(title, "")
	for _, re := range patterns {
		title = re.ReplaceAllString(title, "")
	}
	return extractTrailingYear(strings.TrimSpace(title))
}
```

**Step 4: Run the test to verify it passes**

```bash
cd internal/sync && go test -run TestExtractNameYear -v
```

Expected: all 10 cases `PASS`.

**Step 5: Run the full test suite**

```bash
go test -race ./...
```

Expected: all tests pass.

**Step 6: Commit**

```bash
git add internal/sync/scheduler.go internal/sync/scheduler_test.go
git commit -m "feat: add extractNameYear helper for TMDB year-conflict detection"
```

---

### Task 2: Add year-conflict check in `enrich` + integration test

**Files:**
- Modify: `internal/sync/scheduler.go` — inside `enrich`, in the `if item.TMDBId != ""` block
- Modify: `internal/sync/scheduler_test.go` — add `TestEnrichYearConflict`

---

**Step 1: Write the failing test**

Understanding the test setup: `scheduler_test.go` uses `package sync` (same package), so it can call `enrich` directly. You'll need a mock TMDB server. Look at how existing tests in the file set up httptest servers — follow the same pattern.

Add this test to `internal/sync/scheduler_test.go`:

```go
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

	// GET /movie/620/external_ids — called first, gives wrong IMDB ID
	mux.HandleFunc("/movie/620/external_ids", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"imdb_id": "tt0087332",
		})
	})

	// GET /search/movie?query=Ghostbusters&year=2016 — title retry
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
		t.Fatalf("enrich error: %v", err)
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
}
```

This test requires `tmdb.Client` to expose `SetBaseURL`. Check `internal/tmdb/client.go` — if it only has `baseURL` as a struct field (set in tests via `tc.baseURL = srv.URL`), use that directly since the test is in the same module (but different package). You may need to add a `SetBaseURL(u string)` method or make the field exported. Check the existing `client_test.go` to see how it currently overrides the base URL and follow the same pattern.

**Step 2: Run the test to verify it fails**

```bash
cd internal/sync && go test -run TestEnrichYearConflict -v
```

Expected: compile error or `FAIL` — year-conflict logic not yet present.

**Step 3: Add the year-conflict check to `enrich`**

In `internal/sync/scheduler.go`, inside the `enrich` goroutine, find the block that fetches canonical name + year (currently around line 619):

```go
// Fetch CanonicalName, RuntimeMins, and Year by ID when still
// missing ...
if item.CanonicalName == "" || item.Year == "" {
    var canonTitle string
    var titleErr error
    switch item.Type {
    case index.TypeMovie:
        var movieDetails *tmdb.MovieDetails
        movieDetails, titleErr = s.tmdb.GetMovieDetails(ctx, tmdbID)
        if movieDetails != nil {
            canonTitle = movieDetails.Title
            item.RuntimeMins = movieDetails.RuntimeMins
            if item.ReleaseDate == "" && movieDetails.ReleaseDate != "" {
                item.ReleaseDate = movieDetails.ReleaseDate
            }
            if item.Year == "" && len(movieDetails.ReleaseDate) >= 4 {
                item.Year = movieDetails.ReleaseDate[:4]
            }
        }
    // ...
    }
}
```

Directly **after** that entire `if item.CanonicalName == "" || item.Year == ""` block, add:

```go
// Year-conflict check: if the raw name contains an explicit trailing
// year that differs from the TMDB result year, the provider TMDBId
// is pointing to the wrong movie (e.g. a remake vs. the original).
// Clear the provider IDs and retry via title search with the name year.
if item.Type == index.TypeMovie && providerTMDBId != "" {
    nameYear := extractNameYear(item.Name, s.userPatterns)
    tmdbYear := ""
    if len(item.ReleaseDate) >= 4 {
        tmdbYear = item.ReleaseDate[:4]
    }
    if nameYear != "" && tmdbYear != "" && nameYear != tmdbYear {
        slog.Debug("provider TMDBId year conflict, retrying via title search",
            "name", item.Name,
            "name_year", nameYear,
            "tmdb_year", tmdbYear,
            "provider_tmdb_id", providerTMDBId,
        )
        item.TMDBId = ""
        item.CanonicalName = ""
        item.IMDBId = ""
        item.TVDBId = ""
        item.Year = nameYear
        if err := s.resolveByTitle(ctx, item); err != nil {
            slog.Debug("year-conflict title retry failed", "name", item.Name, "error", err)
        }
        if item.TMDBId != "" {
            retryID, err := strconv.Atoi(item.TMDBId)
            if err == nil && retryID > 0 {
                retryExtIDs, err := s.tmdb.GetMovieExternalIDs(ctx, retryID)
                if err != nil {
                    slog.Debug("year-conflict external ids fetch failed", "tmdb_id", retryID, "error", err)
                } else if retryExtIDs != nil {
                    if retryExtIDs.IMDBID != "" {
                        item.IMDBId = retryExtIDs.IMDBID
                    }
                }
            }
        }
    }
}
```

**Step 4: Run the test to verify it passes**

```bash
cd internal/sync && go test -run TestEnrichYearConflict -v
```

Expected: `PASS`.

**Step 5: Run the full test suite**

```bash
go test -race ./...
```

Expected: all tests pass.

**Step 6: Commit**

```bash
git add internal/sync/scheduler.go internal/sync/scheduler_test.go
git commit -m "fix: detect provider TMDBId year conflicts and retry with name year"
```

---

### Task 3: Verify on live data + release

**Step 1: Build and deploy**

Tag and push to trigger the GitHub Actions release build:

```bash
# Bump version (check current: git tag --sort=-v:refname | head -1)
git tag v0.3.18
git push origin main --tags
```

Wait for the GitHub Actions build to complete, then deploy via Portainer stack 82 → Re-pull image and redeploy.

**Step 2: Trigger a sync and verify**

```bash
# Trigger manual sync
curl -s -X POST http://192.168.1.141:9094/api/sync

# Wait for sync to complete (check status)
curl -s http://192.168.1.141:9094/api/status | python3 -c 'import sys,json; s=json.load(sys.stdin); print(s)'
```

**Step 3: Check the previously-mismatched movies**

```bash
curl -s http://192.168.1.141:9094/api/content/movies > /tmp/after.json
python3 << 'EOF'
import json
with open("/tmp/after.json") as f:
    data = json.load(f)
movies = data.get("items", data)

checks = [
    ("Ghostbusters", "2016", "tt1289401"),   # Ghostbusters: Answer the Call
    ("Aladdin", "2019", "tt6139732"),         # Aladdin 2019
    ("Halloween", "1978", "tt0077651"),       # Halloween 1978 original
    ("Death Wish", "1974", "tt0071360"),      # Death Wish 1974 original
]

for canon, year, expected_imdb in checks:
    matches = [m for m in movies if canon.lower() in m.get("CanonicalName","").lower() and m.get("Year","") == year]
    for m in matches:
        status = "✓" if m.get("IMDBId") == expected_imdb else "✗"
        print(f"{status} {m['CanonicalName']} ({year}): {m.get('IMDBId','')} (want {expected_imdb})")
EOF
```

Expected: all entries show `✓`.
