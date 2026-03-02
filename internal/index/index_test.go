package index

import (
	"sync"
	"testing"
)

// ---- SearchByTVDB ----

func TestSearchByTVDB(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeSeries, XtreamID: 1, Name: "Breaking Bad", TVDBId: "81189"},
		{Type: TypeSeries, XtreamID: 2, Name: "The Wire", TVDBId: "79126"},
	})

	results := idx.SearchByTVDB("81189")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Breaking Bad" {
		t.Errorf("name = %q, want Breaking Bad", results[0].Name)
	}
}

func TestSearchByTVDBUnknownID(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeSeries, XtreamID: 1, Name: "Breaking Bad", TVDBId: "81189"},
	})

	results := idx.SearchByTVDB("99999")
	if len(results) != 0 {
		t.Errorf("expected empty result for unknown TVDB ID, got %d items", len(results))
	}
}

func TestSearchByTVDBMultipleItems(t *testing.T) {
	// Two entries sharing the same TVDB ID (e.g. duplicate streams from provider)
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeSeries, XtreamID: 1, Name: "Series Duplicate A", TVDBId: "11111"},
		{Type: TypeSeries, XtreamID: 2, Name: "Series Duplicate B", TVDBId: "11111"},
	})

	results := idx.SearchByTVDB("11111")
	if len(results) != 2 {
		t.Errorf("expected 2 results for shared TVDB ID, got %d", len(results))
	}
}

// ---- SearchByTMDB ----

func TestSearchByTMDB(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "The Matrix", TMDBId: "603"},
		{Type: TypeSeries, XtreamID: 2, Name: "Breaking Bad", TMDBId: "1396"},
	})

	movies := idx.SearchByTMDB("603")
	if len(movies) != 1 {
		t.Fatalf("expected 1 result for TMDB 603, got %d", len(movies))
	}
	if movies[0].Name != "The Matrix" {
		t.Errorf("name = %q, want The Matrix", movies[0].Name)
	}

	tv := idx.SearchByTMDB("1396")
	if len(tv) != 1 {
		t.Fatalf("expected 1 result for TMDB 1396, got %d", len(tv))
	}
	if tv[0].Name != "Breaking Bad" {
		t.Errorf("name = %q, want Breaking Bad", tv[0].Name)
	}
}

func TestSearchByTMDBUnknownID(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Some Movie", TMDBId: "999"},
	})

	results := idx.SearchByTMDB("0")
	if len(results) != 0 {
		t.Errorf("expected empty for unknown TMDB ID, got %d", len(results))
	}
}

// ---- All() ----

func TestAll(t *testing.T) {
	idx := New()
	items := []*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Movie A"},
		{Type: TypeSeries, XtreamID: 2, Name: "Series A"},
	}
	idx.Replace(items)

	all := idx.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d items, want 2", len(all))
	}
}

func TestAllReturnsIndependentCopy(t *testing.T) {
	// Modifying the slice returned by All() must not affect subsequent calls.
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Movie A"},
		{Type: TypeMovie, XtreamID: 2, Name: "Movie B"},
	})

	snapshot1 := idx.All()
	snapshot1[0] = nil // mutate the copy

	snapshot2 := idx.All()
	if snapshot2[0] == nil {
		t.Error("All() returned a slice that shares backing array — mutations leak between callers")
	}
}

// ---- SearchByTitle year filter ----

func TestSearchByTitleYearFilter(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "The Fly", Year: "1986"},
		{Type: TypeMovie, XtreamID: 2, Name: "The Fly", Year: "1958"},
	})

	results := idx.SearchByTitle("The Fly", "1986", TypeMovie, 10)
	for _, r := range results {
		if r.Year != "1986" {
			t.Errorf("year filter leaked item from year %q", r.Year)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least one result for year 1986")
	}
}

// ---- SearchByTitle empty query (browse mode) ----

func TestSearchByTitleEmptyQueryReturnsItems(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Alpha"},
		{Type: TypeMovie, XtreamID: 2, Name: "Beta"},
		{Type: TypeSeries, XtreamID: 3, Name: "Gamma"},
	})

	all := idx.SearchByTitle("", "", "", 10)
	if len(all) != 3 {
		t.Errorf("empty query should return all 3 items, got %d", len(all))
	}
}

func TestSearchByTitleEmptyQueryTypeFilter(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Movie One"},
		{Type: TypeSeries, XtreamID: 2, Name: "Series One"},
	})

	movies := idx.SearchByTitle("", "", TypeMovie, 10)
	for _, r := range movies {
		if r.Type != TypeMovie {
			t.Errorf("empty-query type filter leaked series %q", r.Name)
		}
	}
	if len(movies) != 1 {
		t.Errorf("expected 1 movie, got %d", len(movies))
	}
}

func TestSearchByTitleEmptyQueryRespectsMaxResults(t *testing.T) {
	items := make([]*Item, 20)
	for i := range items {
		items[i] = &Item{Type: TypeMovie, XtreamID: i + 1, Name: "Movie"}
	}
	idx := New()
	idx.Replace(items)

	results := idx.SearchByTitle("", "", TypeMovie, 5)
	if len(results) != 5 {
		t.Errorf("maxResults not respected: got %d, want 5", len(results))
	}
}

// ---- SearchByTitle default maxResults ----

func TestSearchByTitleDefaultMaxResults(t *testing.T) {
	items := make([]*Item, 30)
	for i := range items {
		items[i] = &Item{Type: TypeMovie, XtreamID: i + 1, Name: "Film"}
	}
	idx := New()
	idx.Replace(items)

	results := idx.SearchByTitle("Film", "", TypeMovie, 0) // 0 → use default (20)
	if len(results) > 20 {
		t.Errorf("default maxResults exceeded: got %d", len(results))
	}
}

// ---- Concurrent Replace safety ----

func TestConcurrentReplace(t *testing.T) {
	idx := New()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Replace([]*Item{
				{Type: TypeMovie, XtreamID: id, Name: "Movie", IMDBId: "tt1234567"},
			})
		}(i)
	}
	wg.Wait()
	// Verify index is usable after concurrent replaces
	results := idx.SearchByIMDB("tt1234567")
	if len(results) == 0 {
		t.Error("index unusable after concurrent Replace calls")
	}
}

// ---- Replace clears old data ----

func TestReplaceErasesOldEntries(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "Old Movie", IMDBId: "tt0000001"},
	})

	// Replace with completely different content
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 2, Name: "New Movie", IMDBId: "tt0000002"},
	})

	old := idx.SearchByIMDB("tt0000001")
	if len(old) != 0 {
		t.Error("old entry still present after Replace — index not cleared")
	}
	newResults := idx.SearchByIMDB("tt0000002")
	if len(newResults) != 1 {
		t.Errorf("new entry not found after Replace: got %d results", len(newResults))
	}
}

// ---- Items without IDs are excluded from ID maps ----

func TestItemsWithoutIDsNotInMaps(t *testing.T) {
	idx := New()
	idx.Replace([]*Item{
		{Type: TypeMovie, XtreamID: 1, Name: "No IDs Movie"}, // no IMDB/TVDB/TMDB
	})

	if results := idx.SearchByIMDB(""); len(results) != 0 {
		t.Error("empty IMDBId should not be indexed")
	}
	if results := idx.SearchByTVDB(""); len(results) != 0 {
		t.Error("empty TVDBId should not be indexed")
	}
	if results := idx.SearchByTMDB(""); len(results) != 0 {
		t.Error("empty TMDBId should not be indexed")
	}
}
