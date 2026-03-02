package newznab

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vodarr/vodarr/internal/index"
)

// makeExtendedTestHandler builds a handler with a richer fixture set covering
// both movies and multi-episode series, with all ID types populated.
func makeExtendedTestHandler() *Handler {
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
				{EpisodeID: 101, Season: 1, EpisodeNum: 1, Title: "Pilot", Ext: "mkv"},
				{EpisodeID: 102, Season: 1, EpisodeNum: 2, Title: "Cat's in the Bag", Ext: "mkv"},
				{EpisodeID: 201, Season: 2, EpisodeNum: 1, Title: "Seven Thirty-Seven", Ext: "mkv"},
			},
		},
	})
	return NewHandler(idx, "", "http://localhost:7878")
}

// ---- TMDB ID search ----

func TestHandleMovieSearchByTMDB(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=movie&tmdbid=603", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var rss RSS
	if err := xml.Unmarshal(w.Body.Bytes(), &rss); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rss.Channel.Items) != 1 {
		t.Fatalf("expected 1 result for tmdbid=603, got %d", len(rss.Channel.Items))
	}
	if !strings.Contains(rss.Channel.Items[0].Title, "Matrix") {
		t.Errorf("wrong item: %q", rss.Channel.Items[0].Title)
	}
}

func TestHandleTVSearchByTMDB(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tmdbid=1396", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var rss RSS
	if err := xml.Unmarshal(w.Body.Bytes(), &rss); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Each episode becomes its own RSS item; we have 3 episodes
	if len(rss.Channel.Items) != 3 {
		t.Fatalf("expected 3 episode items for tmdbid=1396, got %d", len(rss.Channel.Items))
	}
	if !strings.Contains(rss.Channel.Items[0].Title, "Breaking") {
		t.Errorf("wrong series: %q", rss.Channel.Items[0].Title)
	}
}

// ---- Season / episode filters ----

func TestHandleTVSearchSeasonFilter(t *testing.T) {
	h := makeExtendedTestHandler()
	// season=2 should return only the Season 2 episode
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=81189&season=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 1 {
		t.Fatalf("season=2 filter: expected 1 item, got %d", len(rss.Channel.Items))
	}
	// The episode title encodes the season as S02Exx
	if !strings.Contains(rss.Channel.Items[0].Title, "S02E") {
		t.Errorf("season=2 item title should contain S02E, got: %q", rss.Channel.Items[0].Title)
	}
}

func TestHandleTVSearchEpisodeFilter(t *testing.T) {
	h := makeExtendedTestHandler()
	// season=1&ep=2 should return only S01E02
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=81189&season=1&ep=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 1 {
		t.Fatalf("ep=2 filter: expected 1 item, got %d", len(rss.Channel.Items))
	}
	if !strings.Contains(rss.Channel.Items[0].Title, "S01E02") {
		t.Errorf("wrong episode returned: %q", rss.Channel.Items[0].Title)
	}
}

func TestHandleTVSearchNoMatchingEpisode(t *testing.T) {
	h := makeExtendedTestHandler()
	// season=9 doesn't exist — should return empty result, not error
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=81189&season=9", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for empty results", w.Code)
	}
	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 0 {
		t.Errorf("expected 0 results for non-existent season, got %d", len(rss.Channel.Items))
	}
}

// ---- Zero results (unknown IDs) ----

func TestHandleMovieSearchUnknownIMDB(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=movie&imdbid=tt0000000", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Must return 200 with empty channel, not an error status
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 0 {
		t.Errorf("expected 0 results, got %d", len(rss.Channel.Items))
	}
}

func TestHandleTVSearchUnknownTVDB(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=0", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) != 0 {
		t.Errorf("expected 0 results for unknown TVDB ID, got %d", len(rss.Channel.Items))
	}
}

// ---- Empty text query (browse / RSS mode) ----

func TestHandleTextSearchEmptyQueryReturnsItems(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=search&q=", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) == 0 {
		t.Error("empty query should return items in browse/RSS mode")
	}
}

// ---- API key via X-Api-Key header ----

func TestAPIKeyViaHeader(t *testing.T) {
	idx := index.New()
	h := NewHandler(idx, "mySecret", "http://localhost:7878")

	// No key → 401
	req1 := httptest.NewRequest("GET", "/api?t=caps", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("without key via header: status = %d, want 401", w1.Code)
	}

	// Key in X-Api-Key header → 200
	req2 := httptest.NewRequest("GET", "/api?t=caps", nil)
	req2.Header.Set("X-Api-Key", "mySecret")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("with X-Api-Key header: status = %d, want 200", w2.Code)
	}
}

// ---- t=get episode_id param ----

func TestHandleGetSingleEpisodeByID(t *testing.T) {
	h := makeExtendedTestHandler()
	// Request only episode 102 (S01E02 "Cat's in the Bag")
	req := httptest.NewRequest("GET", "/api?t=get&id=2&type=series&episode_id=102", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "102") {
		t.Errorf("expected episode_id 102 in response body: %s", body)
	}
}

func TestHandleGetUnknownEpisodeID(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=get&id=2&type=series&episode_id=9999", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown episode_id: status = %d, want 404", w.Code)
	}
}

// ---- t=get unknown Xtream ID ----

func TestHandleGetUnknownXtreamID(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=get&id=9999&type=movie", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown xtream id: status = %d, want 404", w.Code)
	}
}

// ---- RSS attribute content ----

func TestMovieRSSItemHasIMDBAttr(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=movie&imdbid=tt0133093", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) == 0 {
		t.Fatal("no results")
	}

	// Go's xml.Unmarshal does not resolve namespace prefixes so Attrs is always
	// empty after decode. Check the raw XML body for the attribute elements instead.
	body := w.Body.String()
	if !strings.Contains(body, `name="imdb"`) {
		t.Error("movie RSS item missing newznab:attr imdb")
	}
	if !strings.Contains(body, `name="category"`) {
		t.Error("movie RSS item missing newznab:attr category")
	}
}

func TestTVRSSItemHasTVDBAttr(t *testing.T) {
	h := makeExtendedTestHandler()
	req := httptest.NewRequest("GET", "/api?t=tvsearch&tvdbid=81189", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rss RSS
	xml.Unmarshal(w.Body.Bytes(), &rss)
	if len(rss.Channel.Items) == 0 {
		t.Fatal("no results")
	}

	// Go's xml.Unmarshal does not resolve namespace prefixes so Attrs is always
	// empty after decode. Check the raw XML body for the attribute elements instead.
	body := w.Body.String()
	if !strings.Contains(body, `name="tvdbid"`) {
		t.Error("TV RSS item missing newznab:attr tvdbid")
	}
	if !strings.Contains(body, `name="season"`) {
		t.Error("TV RSS item missing newznab:attr season")
	}
	if !strings.Contains(body, `name="episode"`) {
		t.Error("TV RSS item missing newznab:attr episode")
	}
}
