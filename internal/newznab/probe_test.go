package newznab

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/vodarr/vodarr/internal/index"
)

// mockURLBuilder records calls to EstimateMovieFileSize and returns a fixed size.
type mockURLBuilder struct {
	size      int64
	callCount int64
}

func (m *mockURLBuilder) StreamURL(int, string) string      { return "" }
func (m *mockURLBuilder) SeriesStreamURL(int, string) string { return "" }
func (m *mockURLBuilder) EstimateMovieFileSize(_ context.Context, _ int) int64 {
	atomic.AddInt64(&m.callCount, 1)
	return m.size
}

func newMockHandler(size int64) (*Handler, *mockURLBuilder) {
	mock := &mockURLBuilder{size: size}
	h := &Handler{urls: mock}
	return h, mock
}

func TestProbeItemSizes_Movie(t *testing.T) {
	const wantSize = int64(6_262_800_000)
	h, _ := newMockHandler(wantSize)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 42, ContainerExt: "mkv"},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].FileSize != wantSize {
		t.Errorf("FileSize = %d, want %d", items[0].FileSize, wantSize)
	}
}

func TestProbeItemSizes_ZeroFromProvider(t *testing.T) {
	// Provider returns 0 (no metadata) — FileSize stays 0, fallback used in RSS.
	h, _ := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, ContainerExt: "mkv"},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].FileSize != 0 {
		t.Errorf("FileSize = %d, want 0 when provider returns no metadata", items[0].FileSize)
	}
}

func TestProbeItemSizes_CacheHit(t *testing.T) {
	h, mock := newMockHandler(5_000_000_000)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 99, ContainerExt: "mkv"},
	}

	// First call: should call EstimateMovieFileSize once.
	h.probeItemSizes(context.Background(), items, 0, 0)
	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Fatalf("first call: expected 1 API call, got %d", mock.callCount)
	}

	// Second call: cache hit — no new API call.
	items[0].FileSize = 0 // reset to verify cache restores it
	h.probeItemSizes(context.Background(), items, 0, 0)
	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Errorf("second call: expected still 1 API call (cache hit), got %d", mock.callCount)
	}
	if items[0].FileSize != 5_000_000_000 {
		t.Errorf("FileSize after cache hit = %d, want 5000000000", items[0].FileSize)
	}
}

func TestProbeItemSizes_SeriesNotProbed(t *testing.T) {
	// Series items are skipped — sizes come from sync-time NUMBER_OF_BYTES tag.
	h, mock := newMockHandler(1_000_000_000)
	items := []*index.Item{
		{
			Type:     index.TypeSeries,
			XtreamID: 5,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 1, Season: 1, EpisodeNum: 1, FileSize: 1_917_160_530},
			},
		},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 0 {
		t.Errorf("series should not trigger any API calls, got %d", mock.callCount)
	}
	// Sync-time size must be preserved.
	if items[0].Episodes[0].FileSize != 1_917_160_530 {
		t.Errorf("episode FileSize = %d, want 1917160530 (sync-time value)", items[0].Episodes[0].FileSize)
	}
}

func TestProbeItemSizes_Cap50(t *testing.T) {
	h, mock := newMockHandler(2_000_000_000)

	items := make([]*index.Item, 60)
	for i := range items {
		items[i] = &index.Item{
			Type:         index.TypeMovie,
			XtreamID:     i + 1,
			ContainerExt: "mkv",
		}
	}

	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 50 {
		t.Errorf("expected exactly 50 API calls (cap), got %d", mock.callCount)
	}
	for i := 0; i < 50; i++ {
		if items[i].FileSize == 0 {
			t.Errorf("item[%d] FileSize should be non-zero", i)
		}
	}
	for i := 50; i < 60; i++ {
		if items[i].FileSize != 0 {
			t.Errorf("item[%d] FileSize should be 0 (cap reached)", i)
		}
	}
}
