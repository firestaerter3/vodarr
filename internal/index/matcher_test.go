package index

import "testing"

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
