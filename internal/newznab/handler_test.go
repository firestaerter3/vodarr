package newznab

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vodarr/vodarr/internal/bencode"
	"github.com/vodarr/vodarr/internal/index"
)

func makeTestHandler() *Handler {
	idx := index.New()
	idx.Replace([]*index.Item{
		{
			Type:     index.TypeMovie,
			XtreamID: 1,
			Name:     "The Matrix",
			Year:     "1999",
			IMDBId:   "tt0133093",
			TMDBId:   "603",
		},
		{
			Type:     index.TypeSeries,
			XtreamID: 2,
			Name:     "Breaking Bad",
			Year:     "2008",
			TVDBId:   "81189",
			TMDBId:   "1396",
			Episodes: []index.EpisodeItem{
				{EpisodeID: 1, Season: 1, EpisodeNum: 1, Title: "Pilot"},
			},
		},
	})
	return NewHandler(idx, "", "http://localhost:7878")
}

func TestHandleCaps(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=caps", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Parse and verify precise values — string-contains checks miss regressions in
	// numeric fields like Default/Max and specific supportedParams entries.
	var caps CapsResponse
	if err := xml.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Fatalf("unmarshal caps: %v", err)
	}
	if caps.Server.Title != "VODarr" {
		t.Errorf("server title = %q, want VODarr", caps.Server.Title)
	}
	if caps.Limits.Default != 100 {
		t.Errorf("limits.default = %d, want 100", caps.Limits.Default)
	}
	if caps.Limits.Max != 100 {
		t.Errorf("limits.max = %d, want 100", caps.Limits.Max)
	}
	// Radarr reads supportedParams before deciding which query params to send.
	// Missing "year" means it will never filter by year, reducing precision.
	if !strings.Contains(caps.Searching.Movie.SupportedParams, "year") {
		t.Errorf("movie-search supportedParams %q missing 'year'", caps.Searching.Movie.SupportedParams)
	}
	if !strings.Contains(caps.Searching.Search.SupportedParams, "year") {
		t.Errorf("search supportedParams %q missing 'year'", caps.Searching.Search.SupportedParams)
	}
}

func TestHandleMovieSearchByIMDB(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=movie&imdbid=tt0133093", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var rss RSS
	if err := xml.Unmarshal(w.Body.Bytes(), &rss); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rss.Channel.Items) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rss.Channel.Items))
	}
	if !strings.Contains(rss.Channel.Items[0].Title, "Matrix") {
		t.Errorf("wrong item returned: %q", rss.Channel.Items[0].Title)
	}
}

func TestHandleMovieSearchByIMDBWithoutTT(t *testing.T) {
	h := makeTestHandler()
	// Radarr sometimes sends without "tt" prefix
	req := httptest.NewRequest("GET", "/api?t=movie&imdbid=0133093", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 1 {
		t.Errorf("expected 1 result for imdbid without tt prefix, got %d", len(rss.Channel.Items))
	}
}

func TestHandleTVSearchByTVDB(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=81189", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rss.Channel.Items))
	}
	if !strings.Contains(rss.Channel.Items[0].Title, "Breaking") {
		t.Errorf("wrong item: %q", rss.Channel.Items[0].Title)
	}
}

func TestHandleTextSearch(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=search&q=Matrix", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) == 0 {
		t.Error("expected at least 1 result for text search 'Matrix'")
	}
}

func TestHandleGet(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=get&id=1&type=movie", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/x-bittorrent" {
		t.Errorf("Content-Type = %q, want application/x-bittorrent", ct)
	}

	// Decode bencode torrent
	decoded, err := bencode.Decode(w.Body.Bytes())
	if err != nil {
		t.Fatalf("bencode decode: %v", err)
	}
	torrent, ok := decoded.(map[string]interface{})
	if !ok {
		t.Fatalf("decoded not a dict, got %T", decoded)
	}

	// Extract JSON descriptor from comment field
	comment, ok := torrent["comment"].(string)
	if !ok {
		t.Fatalf("torrent missing comment field")
	}
	var desc map[string]interface{}
	if err := json.Unmarshal([]byte(comment), &desc); err != nil {
		t.Fatalf("comment JSON unmarshal: %v", err)
	}
	if _, ok := desc["xtream_id"]; !ok {
		t.Error("descriptor missing xtream_id")
	}

	// Info dict must be present and contain expected fields
	infoRaw, ok := torrent["info"]
	if !ok {
		t.Fatal("torrent missing info dict")
	}
	info, ok := infoRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("info not a dict, got %T", infoRaw)
	}
	if !strings.HasPrefix(info["name"].(string), "vodarr-") {
		t.Errorf("info.name = %q, want vodarr- prefix", info["name"])
	}
}

func TestCategoryRangeMatch(t *testing.T) {
	cases := []struct {
		cat      string
		low, hi  int
		want     bool
	}{
		// Movies range 2000-2999
		{"2000", 2000, 2999, true},
		{"2040", 2000, 2999, true},
		{"2045", 2000, 2999, true},  // UHD Movies — must NOT trigger TV
		{"5000", 2000, 2999, false}, // TV category
		// TV range 5000-5999
		{"5000", 5000, 5999, true},
		{"5040", 5000, 5999, true},
		{"2045", 5000, 5999, false}, // UHD Movies — must NOT trigger TV
		// Comma-separated
		{"5000,2000", 5000, 5999, true},
		{"5000,2000", 2000, 2999, true},
		// Empty / non-numeric
		{"", 2000, 2999, false},
		{"abc", 2000, 2999, false},
	}
	for _, c := range cases {
		got := categoryRangeMatch(c.cat, c.low, c.hi)
		if got != c.want {
			t.Errorf("categoryRangeMatch(%q, %d, %d) = %v, want %v", c.cat, c.low, c.hi, got, c.want)
		}
	}
}

func TestHandleTextSearchCategoryFilter(t *testing.T) {
	h := makeTestHandler()

	// cat=2045 (UHD Movies) should return movies, not TV.
	req := httptest.NewRequest("GET", "/api?t=search&q=matrix&cat=2045", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)

	// Positive: must return at least one result (the Matrix movie).
	if len(rss.Channel.Items) == 0 {
		t.Error("cat=2045 should return at least one movie result")
	}
	// Negative: no TV items.
	for _, item := range rss.Channel.Items {
		for _, attr := range item.Attrs {
			if attr.Name == "category" && attr.Value == "5000" {
				t.Errorf("cat=2045 should not return TV item, got: %q", item.Title)
			}
		}
	}
}

func TestHandleMovieSearchTypeFilter(t *testing.T) {
	// Both a movie and a series share the same IMDB ID (unlikely but possible).
	// Movie search (t=movie) must return only the movie.
	idx := index.New()
	idx.Replace([]*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, Name: "Shared IMDB Movie", IMDBId: "tt9999999"},
		{Type: index.TypeSeries, XtreamID: 2, Name: "Shared IMDB Series", IMDBId: "tt9999999",
			Episodes: []index.EpisodeItem{{EpisodeID: 1, Season: 1, EpisodeNum: 1}}},
	})
	h := NewHandler(idx, "", "http://localhost:7878")

	req := httptest.NewRequest("GET", "/api?t=movie&imdbid=tt9999999", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)

	// Positive: exactly one result (the movie).
	if len(rss.Channel.Items) != 1 {
		t.Errorf("expected 1 movie result, got %d", len(rss.Channel.Items))
	}
	if len(rss.Channel.Items) > 0 && !strings.Contains(rss.Channel.Items[0].Title, "Shared.IMDB.Movie") {
		t.Errorf("wrong item returned: %q", rss.Channel.Items[0].Title)
	}
	// Negative: no TV items.
	for _, item := range rss.Channel.Items {
		for _, attr := range item.Attrs {
			if attr.Name == "category" && attr.Value == "5000" {
				t.Errorf("movie search should not return TV item: %q", item.Title)
			}
		}
	}
}

func TestHandleGetZeroEpisodes(t *testing.T) {
	// Series with no episodes should return 404 from t=get
	idx := index.New()
	idx.Replace([]*index.Item{
		{Type: index.TypeSeries, XtreamID: 99, Name: "Empty Series"},
	})
	h := NewHandler(idx, "", "http://localhost:7878")

	req := httptest.NewRequest("GET", "/api?t=get&id=99&type=series", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("t=get for zero-episode series: status = %d, want 404", w.Code)
	}
}

func TestHandleInvalidAction(t *testing.T) {
	h := makeTestHandler()
	req := httptest.NewRequest("GET", "/api?t=nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAPIKeyRequired(t *testing.T) {
	idx := index.New()
	h := NewHandler(idx, "secret123", "http://localhost:7878")

	req := httptest.NewRequest("GET", "/api?t=caps", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("without key: status = %d, want 401", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/api?t=caps&apikey=secret123", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("with correct key: status = %d, want 200", w2.Code)
	}
}
