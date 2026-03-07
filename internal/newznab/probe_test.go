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

func TestProbeItemSizes_TMDBRuntimeFallback_Default(t *testing.T) {
	// Provider returns 0; item has RuntimeMins — fallback uses 6 Mbps (default 1080p H.264).
	// Expected: 750_000 B/s * 120 min * 60 s/min = 5_400_000_000
	h, mock := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 10, ContainerExt: "mkv", RuntimeMins: 120},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Fatalf("expected 1 API call, got %d", mock.callCount)
	}
	const want = int64(750_000 * 120 * 60)
	if items[0].FileSize != want {
		t.Errorf("FileSize = %d, want %d (TMDB runtime fallback)", items[0].FileSize, want)
	}
}

func TestProbeItemSizes_TMDBRuntimeFallback_HEVC(t *testing.T) {
	// Provider returns 0; item name contains HEVC — fallback uses 4 Mbps.
	// Expected: 500_000 B/s * 120 min * 60 s/min = 3_600_000_000
	h, mock := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 11, Name: "The Movie HEVC 1080p", ContainerExt: "mkv", RuntimeMins: 120},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Fatalf("expected 1 API call, got %d", mock.callCount)
	}
	const want = int64(500_000 * 120 * 60)
	if items[0].FileSize != want {
		t.Errorf("FileSize = %d, want %d (HEVC fallback)", items[0].FileSize, want)
	}
}

func TestProbeItemSizes_TMDBRuntimeFallback_4K(t *testing.T) {
	// Provider returns 0; item name contains 4K — fallback uses 12 Mbps.
	// Expected: 1_500_000 B/s * 90 min * 60 s/min = 8_100_000_000
	h, mock := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 12, Name: "Action Movie 4K", ContainerExt: "mkv", RuntimeMins: 90},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Fatalf("expected 1 API call, got %d", mock.callCount)
	}
	const want = int64(1_500_000 * 90 * 60)
	if items[0].FileSize != want {
		t.Errorf("FileSize = %d, want %d (4K fallback)", items[0].FileSize, want)
	}
}

func TestProbeItemSizes_ZeroRuntimeMinsStaysZero(t *testing.T) {
	// Provider returns 0 and RuntimeMins is 0 — FileSize stays 0.
	h, _ := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 13, ContainerExt: "mkv", RuntimeMins: 0},
	}
	h.probeItemSizes(context.Background(), items, 0, 0)

	if items[0].FileSize != 0 {
		t.Errorf("FileSize = %d, want 0 when provider returns 0 and RuntimeMins is 0", items[0].FileSize)
	}
}

func TestProbeItemSizes_ProviderRetryAfterZero(t *testing.T) {
	// When provider returns 0 and RuntimeMins is 0, result is not cached,
	// so the next request retries the provider.
	h, mock := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 14, ContainerExt: "mkv"},
	}

	h.probeItemSizes(context.Background(), items, 0, 0)
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 2 {
		t.Errorf("expected 2 API calls (no cache on zero), got %d", mock.callCount)
	}
}

func TestProbeItemSizes_TMDBFallbackIsCached(t *testing.T) {
	// When TMDB fallback is used, the estimated value is cached so the
	// provider is not retried on subsequent requests.
	h, mock := newMockHandler(0)
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 15, ContainerExt: "mkv", RuntimeMins: 100},
	}

	h.probeItemSizes(context.Background(), items, 0, 0)
	items[0].FileSize = 0 // reset to verify cache restores it
	h.probeItemSizes(context.Background(), items, 0, 0)

	if atomic.LoadInt64(&mock.callCount) != 1 {
		t.Errorf("expected 1 API call (TMDB estimate cached), got %d", mock.callCount)
	}
	const want = int64(750_000 * 100 * 60)
	if items[0].FileSize != want {
		t.Errorf("FileSize after cache hit = %d, want %d", items[0].FileSize, want)
	}
}
