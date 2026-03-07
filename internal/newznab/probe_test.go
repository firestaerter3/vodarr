package newznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

// testURLBuilder routes StreamURL and SeriesStreamURL to a test server.
type testURLBuilder struct {
	baseURL string
}

func (m *testURLBuilder) StreamURL(streamID int, ext string) string {
	return fmt.Sprintf("%s/movie/%d.%s", m.baseURL, streamID, ext)
}

func (m *testURLBuilder) SeriesStreamURL(episodeID int, ext string) string {
	return fmt.Sprintf("%s/episode/%d.%s", m.baseURL, episodeID, ext)
}

func newProbeHandler(baseURL string) *Handler {
	return &Handler{
		urls:     &testURLBuilder{baseURL: baseURL},
		headHTTP: &http.Client{Timeout: 3 * time.Second},
	}
}

func TestProbeItemSizes_Movie(t *testing.T) {
	const wantSize = int64(1_234_567_890)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1234567890")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 42, ContainerExt: "mkv"},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].FileSize != wantSize {
		t.Errorf("FileSize = %d, want %d", items[0].FileSize, wantSize)
	}
}

func TestProbeItemSizes_Episode(t *testing.T) {
	const wantSize = int64(987_654_321)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "987654321")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{
			Type:     index.TypeSeries,
			XtreamID: 10,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 101, Season: 1, EpisodeNum: 1, Ext: "mkv"},
			},
		},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].Episodes[0].FileSize != wantSize {
		t.Errorf("Episode FileSize = %d, want %d", items[0].Episodes[0].FileSize, wantSize)
	}
}

func TestProbeItemSizes_CacheHit(t *testing.T) {
	var headCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&headCount, 1)
		w.Header().Set("Content-Length", "111111111")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 99, ContainerExt: "mkv"},
	}

	// First call: should probe.
	h.probeItemSizes(context.Background(), items, 0, 0)
	if atomic.LoadInt64(&headCount) != 1 {
		t.Fatalf("first call: expected 1 HEAD request, got %d", headCount)
	}

	// Second call: should be served from cache, no new HEAD.
	items[0].FileSize = 0 // reset to verify cache restores it
	h.probeItemSizes(context.Background(), items, 0, 0)
	if atomic.LoadInt64(&headCount) != 1 {
		t.Errorf("second call: expected still 1 HEAD request (cache hit), got %d", headCount)
	}
	if items[0].FileSize != 111_111_111 {
		t.Errorf("FileSize after cache hit = %d, want 111111111", items[0].FileSize)
	}
}

func TestProbeItemSizes_ServerError_LeavesZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, ContainerExt: "mkv"},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	// Content-Length absent on 500 → FileSize stays 0.
	if items[0].FileSize != 0 {
		t.Errorf("FileSize = %d, want 0 on server error", items[0].FileSize)
	}
}

func TestProbeItemSizes_SmallHTMLResponse_LeavesZero(t *testing.T) {
	// Providers that don't support HEAD return a small HTML error page.
	// Content-Length below 1 MB must be rejected.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "858")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, ContainerExt: "mkv"},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].FileSize != 0 {
		t.Errorf("FileSize = %d, want 0 for small HTML response", items[0].FileSize)
	}
}

func TestProbeItemSizes_SeasonFilter(t *testing.T) {
	var headCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&headCount, 1)
		w.Header().Set("Content-Length", "500000000")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)
	items := []*index.Item{
		{
			Type:     index.TypeSeries,
			XtreamID: 5,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 1, Season: 1, EpisodeNum: 1, Ext: "mkv"},
				{EpisodeID: 2, Season: 1, EpisodeNum: 2, Ext: "mkv"},
				{EpisodeID: 3, Season: 2, EpisodeNum: 1, Ext: "mkv"}, // filtered out
			},
		},
	}

	// seasonFilter=1: only probe season 1 episodes.
	h.probeItemSizes(context.Background(), items, 1, 0)

	if atomic.LoadInt64(&headCount) != 2 {
		t.Errorf("season filter: expected 2 HEAD requests, got %d", headCount)
	}
	if items[0].Episodes[0].FileSize == 0 {
		t.Error("S01E01 FileSize should be non-zero")
	}
	if items[0].Episodes[1].FileSize == 0 {
		t.Error("S01E02 FileSize should be non-zero")
	}
	if items[0].Episodes[2].FileSize != 0 {
		t.Error("S02E01 FileSize should be 0 (filtered out)")
	}
}

func TestProbeItemSizes_Cap50(t *testing.T) {
	var headCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&headCount, 1)
		w.Header().Set("Content-Length", "100000000")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newProbeHandler(srv.URL)

	// Build 60 movies — probing should stop at 50.
	items := make([]*index.Item, 60)
	for i := range items {
		items[i] = &index.Item{
			Type:         index.TypeMovie,
			XtreamID:     i + 1,
			ContainerExt: "mkv",
		}
	}

	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&headCount) != 50 {
		t.Errorf("expected exactly 50 HEAD requests (cap), got %d", headCount)
	}
	// First 50 items have size set.
	for i := 0; i < 50; i++ {
		if items[i].FileSize == 0 {
			t.Errorf("item[%d] FileSize should be non-zero", i)
		}
	}
	// Items 50-59 should remain 0 (cap reached).
	for i := 50; i < 60; i++ {
		if items[i].FileSize != 0 {
			t.Errorf("item[%d] FileSize should be 0 (cap reached)", i)
		}
	}
}
