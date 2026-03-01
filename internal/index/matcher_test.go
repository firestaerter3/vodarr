package index

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The Dark Knight", "dark knight"},
		{"A Beautiful Mind", "beautiful mind"},
		{"An Inspector Calls", "inspector calls"},
		{"Star Wars: A New Hope", "star wars a new hope"},  // "a" here is mid-title, not leading
		{"  Léon: The Professional  ", "léon the professional"}, // "the" is mid-title
	}
	for _, c := range cases {
		got := normalizeTitle(c.in)
		if got != c.want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	cases := []struct {
		a, b      string
		wantAbove float64 // score should be >= this
	}{
		{"the dark knight", "dark knight", 0.7},
		{"avengers endgame", "avengers endgame", 1.0},
		{"spider man homecoming", "spider-man homecoming", 0.7},
		{"completely different", "nothing in common", 0.0},
	}
	for _, c := range cases {
		score := titleSimilarity(c.a, c.b)
		if score < c.wantAbove {
			t.Errorf("titleSimilarity(%q, %q) = %.3f, want >= %.3f", c.a, c.b, score, c.wantAbove)
		}
	}
}

func TestIndexSearchByTitle(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "The Dark Knight", Year: "2008", IMDBId: "tt0468569"},
		{Type: TypeMovie, XtreamID: 2, Name: "Avengers: Endgame", Year: "2019", IMDBId: "tt4154796"},
		{Type: TypeSeries, XtreamID: 3, Name: "Breaking Bad", Year: "2008", TVDBId: "81189"},
	})

	t.Run("exact movie match", func(t *testing.T) {
		results := idx.SearchByTitle("Dark Knight", "", TypeMovie, 10)
		if len(results) == 0 {
			t.Fatal("expected at least one result")
		}
		if results[0].XtreamID != 1 {
			t.Errorf("top result XtreamID = %d, want 1", results[0].XtreamID)
		}
	})

	t.Run("series type filter", func(t *testing.T) {
		results := idx.SearchByTitle("Breaking Bad", "", TypeSeries, 10)
		if len(results) == 0 {
			t.Fatal("expected at least one result")
		}
		if results[0].Type != TypeSeries {
			t.Errorf("got type %q, want series", results[0].Type)
		}
	})

	t.Run("no cross-type results when filtered", func(t *testing.T) {
		results := idx.SearchByTitle("Breaking Bad", "", TypeMovie, 10)
		for _, r := range results {
			if r.XtreamID == 3 {
				t.Error("series result appeared in movie-only search")
			}
		}
	})
}

func TestIndexSearchByIMDB(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Inception", IMDBId: "tt1375666"},
	})

	results := idx.SearchByIMDB("tt1375666")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].XtreamID != 1 {
		t.Errorf("wrong item returned")
	}

	empty := idx.SearchByIMDB("tt0000000")
	if len(empty) != 0 {
		t.Errorf("expected empty result for unknown IMDB ID")
	}
}

func TestByXtreamNoCollision(t *testing.T) {
	// Xtream Codes uses separate ID spaces for VOD and series.
	// A movie with stream_id=100 and a series with series_id=100 must both
	// be findable — they should NOT overwrite each other in the index.
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 100, Name: "Movie ID 100"},
		{Type: TypeSeries, XtreamID: 100, Name: "Series ID 100"},
	})

	movie := idx.SearchByXtreamID(100, "movie")
	if movie == nil {
		t.Fatal("expected to find movie with XtreamID=100")
	}
	if movie.Type != TypeMovie {
		t.Errorf("movie lookup returned type %q, want movie", movie.Type)
	}

	series := idx.SearchByXtreamID(100, "series")
	if series == nil {
		t.Fatal("expected to find series with XtreamID=100")
	}
	if series.Type != TypeSeries {
		t.Errorf("series lookup returned type %q, want series", series.Type)
	}
}

func TestByXtreamNoTypeHint(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 5, Name: "Just a Movie"},
	})

	// Without a type hint, SearchByXtreamID should still find the item.
	item := idx.SearchByXtreamID(5, "")
	if item == nil {
		t.Fatal("expected to find item without type hint")
	}
	if item.Name != "Just a Movie" {
		t.Errorf("wrong item: %q", item.Name)
	}
}

func TestByXtreamNoTypeHintMovieFirst(t *testing.T) {
	// When both a movie and a series share the same numeric Xtream ID (separate
	// Xtream ID spaces), SearchByXtreamID with no type hint must return the movie
	// because the implementation tries movie before series.
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 200, Name: "Movie 200"},
		{Type: TypeSeries, XtreamID: 200, Name: "Series 200"},
	})
	item := idx.SearchByXtreamID(200, "")
	if item == nil {
		t.Fatal("expected item when both types present and no type hint given")
	}
	if item.Type != TypeMovie {
		t.Errorf("no-hint lookup returned type %q, want movie (movie-first fallback)", item.Type)
	}
}

func TestEpisodeItemJSONTags(t *testing.T) {
	// EpisodeItem field names must be PascalCase in JSON to match the qBit
	// handler's unmarshal struct. If Go's default lowercase marshaling were used,
	// desc.Episodes would silently decode as zero values and no STRM files would
	// be written for series.
	ep := EpisodeItem{
		EpisodeID:  42,
		Season:     3,
		EpisodeNum: 7,
		Title:      "Fly",
		Ext:        "mkv",
	}
	data, err := json.Marshal(ep)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(data)

	for _, want := range []string{`"EpisodeID"`, `"Season"`, `"EpisodeNum"`, `"Title"`, `"Ext"`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshalled JSON missing field %s; got: %s", want, s)
		}
	}

	// Round-trip: values must survive marshal → unmarshal.
	var ep2 EpisodeItem
	if err := json.Unmarshal(data, &ep2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if ep2.EpisodeID != 42 || ep2.Season != 3 || ep2.EpisodeNum != 7 || ep2.Title != "Fly" || ep2.Ext != "mkv" {
		t.Errorf("round-trip mismatch: got %+v", ep2)
	}
}

func TestIndexCounts(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Movie A"},
		{Type: TypeMovie, XtreamID: 2, Name: "Movie B"},
		{Type: TypeSeries, XtreamID: 3, Name: "Series A"},
	})
	movies, series := idx.Counts()
	if movies != 2 {
		t.Errorf("movies = %d, want 2", movies)
	}
	if series != 1 {
		t.Errorf("series = %d, want 1", series)
	}
}
