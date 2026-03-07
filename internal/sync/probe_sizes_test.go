package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/xtream"
)

// newTestScheduler builds a minimal Scheduler for probing tests.
func newTestScheduler(baseURL string) *Scheduler {
	cfg := &config.Config{}
	cfg.Sync.Parallelism = 5
	xc := xtream.NewClient(baseURL, "user", "pass")
	return &Scheduler{cfg: cfg, xtream: xc}
}

func TestProbeSizes_Movie(t *testing.T) {
	const wantSize = int64(1_234_567_890)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1234567890")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestScheduler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 42, ContainerExt: "mkv"},
	}

	s.probeSizes(context.Background(), items, nil)

	if items[0].FileSize != wantSize {
		t.Errorf("FileSize = %d, want %d", items[0].FileSize, wantSize)
	}
}

func TestProbeSizes_Episode(t *testing.T) {
	const wantSize = int64(987_654_321)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "987654321")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestScheduler(srv.URL)
	items := []*index.Item{
		{
			Type:     index.TypeSeries,
			XtreamID: 10,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 101, Season: 1, EpisodeNum: 1, Ext: "mkv"},
			},
		},
	}

	s.probeSizes(context.Background(), items, nil)

	if items[0].Episodes[0].FileSize != wantSize {
		t.Errorf("Episode FileSize = %d, want %d", items[0].Episodes[0].FileSize, wantSize)
	}
}

func TestProbeSizes_SmartSkip_Movie(t *testing.T) {
	// Server should never be called when cache has a FileSize.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestScheduler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 42, ContainerExt: "mkv"},
	}
	cachedByKey := map[string]*index.Item{
		"movie:42": {Type: index.TypeMovie, XtreamID: 42, FileSize: 5_000_000_000},
	}

	s.probeSizes(context.Background(), items, cachedByKey)

	if called {
		t.Error("HEAD request was made despite cached FileSize")
	}
	if items[0].FileSize != 5_000_000_000 {
		t.Errorf("FileSize = %d, want 5000000000", items[0].FileSize)
	}
}

func TestProbeSizes_SmartSkip_Episode(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newTestScheduler(srv.URL)
	items := []*index.Item{
		{
			Type:     index.TypeSeries,
			XtreamID: 10,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 101, Season: 1, EpisodeNum: 1, Ext: "mkv"},
			},
		},
	}
	cachedByKey := map[string]*index.Item{
		"series:10": {
			Type:     index.TypeSeries,
			XtreamID: 10,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 101, FileSize: 3_000_000_000},
			},
		},
	}

	s.probeSizes(context.Background(), items, cachedByKey)

	if called {
		t.Error("HEAD request was made despite cached episode FileSize")
	}
	if items[0].Episodes[0].FileSize != 3_000_000_000 {
		t.Errorf("Episode FileSize = %d, want 3000000000", items[0].Episodes[0].FileSize)
	}
}

func TestProbeSizes_ServerError_LeavesZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestScheduler(srv.URL)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, ContainerExt: "mkv"},
	}

	s.probeSizes(context.Background(), items, nil)

	// Content-Length is absent on 500 responses — FileSize should stay 0.
	if items[0].FileSize != 0 {
		t.Errorf("FileSize = %d, want 0 on server error", items[0].FileSize)
	}
}

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
