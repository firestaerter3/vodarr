package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// newTestClient creates a Client wired to the given test server URL.
func newTestClient(srv *httptest.Server) *Client {
	c := NewClient("testkey")
	c.baseURL = srv.URL
	return c
}

// requireAPIKey is a handler middleware that rejects requests without the expected api_key query param.
func requireAPIKey(t *testing.T, apiKey string, next http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != apiKey {
			t.Errorf("api_key query param = %q, want %q", got, apiKey)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ---- Validate ----

func TestValidate200(t *testing.T) {
	srv := httptest.NewServer(requireAPIKey(t, "validkey", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configuration" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":{}}`))
	}))
	defer srv.Close()

	c := NewClient("validkey")
	c.baseURL = srv.URL

	if err := c.Validate(context.Background()); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidate401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false}`))
	}))
	defer srv.Close()

	c := NewClient("badkey")
	c.baseURL = srv.URL

	err := c.Validate(context.Background())
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}
}

func TestValidate500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("anykey")
	c.baseURL = srv.URL

	err := c.Validate(context.Background())
	if err == nil {
		t.Fatal("Validate() = nil, want error for 500")
	}
}

// ---- SearchMovie ----

func TestSearchMovieReturnsFirstResult(t *testing.T) {
	srv := httptest.NewServer(requireAPIKey(t, "testkey", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"results":[{"id":603,"title":"The Matrix","release_date":"1999-03-31"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchMovie(context.Background(), "The Matrix", 1999)
	if err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	if result == nil {
		t.Fatal("SearchMovie returned nil, want result")
	}
	if result.ID != 603 {
		t.Errorf("result.ID = %d, want 603", result.ID)
	}
	if result.Title != "The Matrix" {
		t.Errorf("result.Title = %q, want %q", result.Title, "The Matrix")
	}
}

func TestSearchMovieEmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchMovie(context.Background(), "NoSuchMovie", 0)
	if err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for no matches, got %+v", result)
	}
}

func TestSearchMovieCacheHitAvoidsHTTP(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Write([]byte(`{"results":[{"id":100,"title":"Cached Movie"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()

	r1, _ := c.SearchMovie(ctx, "Cached Movie", 0)
	r2, _ := c.SearchMovie(ctx, "Cached Movie", 0)

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 HTTP call, got %d (cache miss on second call)", callCount)
	}
	if r1 == nil || r2 == nil {
		t.Fatal("expected non-nil results")
	}
	if r1.ID != r2.ID {
		t.Errorf("r1.ID=%d != r2.ID=%d", r1.ID, r2.ID)
	}
}

func TestSearchMovieNilCachePreventsExtraHTTP(t *testing.T) {
	// A nil result (no matches) should also be cached so the same missing title
	// doesn't cause repeated API calls.
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()

	c.SearchMovie(ctx, "Ghost Title", 0) //nolint
	c.SearchMovie(ctx, "Ghost Title", 0) //nolint

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 HTTP call for nil-result cache, got %d", callCount)
	}
}

// ---- SearchTV ----

func TestSearchTVReturnsFirstResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"results":[{"id":1396,"name":"Breaking Bad","first_air_date":"2008-01-20"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchTV(context.Background(), "Breaking Bad", 2008)
	if err != nil {
		t.Fatalf("SearchTV: %v", err)
	}
	if result == nil {
		t.Fatal("SearchTV returned nil")
	}
	if result.ID != 1396 {
		t.Errorf("result.ID = %d, want 1396", result.ID)
	}
}

func TestSearchTVEmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchTV(context.Background(), "NoSuchShow", 0)
	if err != nil {
		t.Fatalf("SearchTV: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestSearchTVCacheHitAvoidsHTTP(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Write([]byte(`{"results":[{"id":50,"name":"Cached Show"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()
	c.SearchTV(ctx, "Cached Show", 0) //nolint
	c.SearchTV(ctx, "Cached Show", 0) //nolint

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 HTTP call, got %d", callCount)
	}
}

// ---- GetMovieExternalIDs ----

func TestGetMovieExternalIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/603/external_ids" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"imdb_id":"tt0133093","tvdb_id":0}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ids, err := c.GetMovieExternalIDs(context.Background(), 603)
	if err != nil {
		t.Fatalf("GetMovieExternalIDs: %v", err)
	}
	if ids == nil {
		t.Fatal("expected non-nil ids")
	}
	if ids.IMDBID != "tt0133093" {
		t.Errorf("IMDBID = %q, want tt0133093", ids.IMDBID)
	}
	if ids.TMDBId != 603 {
		t.Errorf("TMDBId = %d, want 603", ids.TMDBId)
	}
}

func TestGetMovieExternalIDs404NotCached(t *testing.T) {
	// 404 should NOT be cached — a second call must still hit the server.
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()

	r1, err1 := c.GetMovieExternalIDs(ctx, 9999)
	r2, err2 := c.GetMovieExternalIDs(ctx, 9999)

	if err1 != nil || err2 != nil {
		t.Fatalf("404 should return nil error: %v / %v", err1, err2)
	}
	if r1 != nil || r2 != nil {
		t.Errorf("404 should return nil ids")
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 HTTP calls for 404 (not cached), got %d", callCount)
	}
}

func TestGetMovieExternalIDsCacheHit(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Write([]byte(`{"imdb_id":"tt1111111"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()
	c.GetMovieExternalIDs(ctx, 1) //nolint
	c.GetMovieExternalIDs(ctx, 1) //nolint

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 HTTP call (cache hit on second), got %d", callCount)
	}
}

// ---- GetTVExternalIDs ----

func TestGetTVExternalIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1396/external_ids" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"imdb_id":"tt0903747","tvdb_id":81189}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ids, err := c.GetTVExternalIDs(context.Background(), 1396)
	if err != nil {
		t.Fatalf("GetTVExternalIDs: %v", err)
	}
	if ids == nil {
		t.Fatal("expected non-nil ids")
	}
	if ids.IMDBID != "tt0903747" {
		t.Errorf("IMDBID = %q, want tt0903747", ids.IMDBID)
	}
	if ids.TVDBID != 81189 {
		t.Errorf("TVDBID = %d, want 81189", ids.TVDBID)
	}
	if ids.TMDBId != 1396 {
		t.Errorf("TMDBId = %d, want 1396", ids.TMDBId)
	}
}

func TestGetTVExternalIDs404NotCached(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx := context.Background()

	r1, err1 := c.GetTVExternalIDs(ctx, 8888)
	r2, err2 := c.GetTVExternalIDs(ctx, 8888)

	if err1 != nil || err2 != nil {
		t.Fatalf("404 should return nil error: %v / %v", err1, err2)
	}
	if r1 != nil || r2 != nil {
		t.Errorf("404 should return nil ids")
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 HTTP calls for 404 (not cached), got %d", callCount)
	}
}

// ---- Context cancellation ----

func TestContextCancellationStopsRequest(t *testing.T) {
	// A cancelled context must cause get() to return ctx.Err() rather than
	// waiting for the rate limiter or the HTTP response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.SearchMovie(ctx, "Anything", 0)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}
