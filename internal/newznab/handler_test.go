package newznab

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	body := w.Body.String()
	if !strings.Contains(body, "VODarr") {
		t.Error("caps response missing server title")
	}
	if !strings.Contains(body, "movie-search") {
		t.Error("caps response missing movie-search")
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
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "xtream_id") {
		t.Error("t=get response missing xtream_id")
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
