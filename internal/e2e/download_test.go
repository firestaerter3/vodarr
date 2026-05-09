package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/download"
	"github.com/vodarr/vodarr/internal/qbit"
	"github.com/vodarr/vodarr/internal/xtream"
)

// newHarnessThrottled builds a download-mode harness identical to newHarness
// except that the download manager applies a bandwidth cap. This is used
// exclusively by TestProgressTracking_Download to create a window long enough
// for HTTP polling to observe an intermediate progress value.
//
// Implementation: newHarness provides the shared Xtream mock, Newznab server,
// writer, and tmpDir. We reconstruct the xtream.Client from the mock server URL
// (matching the "user"/"pass" credentials hardcoded in harness_test.go), then
// build a fresh qbit.Handler with a throttled download.Manager and swap it in.
func newHarnessThrottled(t *testing.T, bytesPerSec int64) *harness {
	t.Helper()

	base := newHarness(t, "download")

	// Reconstruct the xtream client using the same credentials as harness_test.go.
	xc := xtream.NewClient(base.xtreamSrv.URL, "user", "pass")

	throttledDL := download.NewManager(download.Options{
		MaxConcurrent:  1,
		InterDelay:     1 * time.Millisecond,
		BandwidthLimit: bytesPerSec,
	})

	store := qbit.NewStore()
	qbitH := qbit.NewHandler(
		store, base.writer, xc, nilProber{},
		base.tmpDir, "", "",
		base.newznabSrv.URL, "download", throttledDL,
	)
	qbitSrv := httptest.NewServer(qbitH)
	t.Cleanup(qbitSrv.Close)

	h2 := *base
	h2.qbitSrv = qbitSrv
	return &h2
}

// TestMovieGrab_URL_Download: full download flow — real file written to disk,
// byte-complete, no .strm sidecar.
func TestMovieGrab_URL_Download(t *testing.T) {
	h := newHarness(t, "download")

	rss := h.searchMovie("tt0133093")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no download URLs in movie search RSS")
	}

	hash := h.grabByURL(urls[0], h.tmpDir, "vodarr-movies")
	// Allow extra time: download mode writes 300 KB from an in-process server.
	if err := h.waitForState(hash, "pausedUP", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	files := h.torrentFiles(hash)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], ".mkv") {
		t.Errorf("expected .mkv file in torrent listing, got %q", files[0])
	}

	// writer.MovieFilePath returns the absolute destination path.
	absPath := h.writer.MovieFilePath("The Matrix", "1999", "mkv")
	rel := h.absToRel(absPath)

	if !h.fileExists(rel) {
		t.Errorf("downloaded file not found at %s", rel)
	}
	if got := h.fileSize(rel); got != int64(len(h.mkvPayload)) {
		t.Errorf("file size = %d bytes, want %d (byte-complete)", got, len(h.mkvPayload))
	}

	// Download mode must NOT write a .strm pointer file
	strmRel := filepath.Join(filepath.Dir(rel), strings.TrimSuffix(filepath.Base(rel), ".mkv")+".strm")
	if h.fileExists(strmRel) {
		t.Errorf(".strm found in download mode (should not exist): %s", strmRel)
	}
}

// TestSeriesGrab_URL_Download: all 3 episodes downloaded with correct byte counts.
func TestSeriesGrab_URL_Download(t *testing.T) {
	h := newHarness(t, "download")

	downloadURL := h.newznabSrv.URL + "/api?t=get&id=2&type=series"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-tv")
	// 3 episodes × 300 KB each; allow 15 s (download manager is sequential).
	if err := h.waitForState(hash, "pausedUP", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	files := h.torrentFiles(hash)
	if len(files) != 3 {
		t.Fatalf("expected 3 file entries, got %d: %v", len(files), files)
	}

	episodes := []struct {
		season, ep int
		title      string
	}{
		{1, 1, "Pilot"},
		{1, 2, "Cat's in the Bag"},
		{2, 1, "Seven Thirty-Seven"},
	}
	for _, ep := range episodes {
		absPath := h.writer.EpisodeFilePath("Breaking Bad", ep.season, ep.ep, ep.title, "mkv")
		rel := h.absToRel(absPath)
		if !h.fileExists(rel) {
			t.Errorf("episode file not found: %s", rel)
			continue
		}
		if got := h.fileSize(rel); got != int64(len(h.mkvPayload)) {
			t.Errorf("episode %s size = %d, want %d", rel, got, len(h.mkvPayload))
		}
	}
}

// TestProgressTracking_Download: download progress is visible via torrents/info
// before the download completes.
//
// Uses a bandwidth-capped harness (50 KB/s) so the first 256 KB chunk takes
// roughly 5 s — long enough for polling to observe progress between 0 and 1.
// All other download tests use the uncapped newHarness for speed.
func TestProgressTracking_Download(t *testing.T) {
	// 50 KB/s → first chunk (256 KB) ≈ 5 s; second chunk (44 KB) ≈ 0.9 s.
	// Polling at 10 ms will catch many intermediate samples.
	h := newHarnessThrottled(t, 50*1024)

	downloadURL := h.newznabSrv.URL + "/api?t=get&id=1&type=movie"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-movies")

	// Collect progress samples until done or timeout.
	var progressSamples []float64
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.qbitSrv.URL + "/api/v2/torrents/info?hashes=" + hash)
		if err != nil {
			break
		}
		var torrents []struct {
			State    string  `json:"state"`
			Progress float64 `json:"progress"`
		}
		json.NewDecoder(resp.Body).Decode(&torrents) //nolint:errcheck
		resp.Body.Close()
		if len(torrents) > 0 {
			progressSamples = append(progressSamples, torrents[0].Progress)
			if torrents[0].State == "pausedUP" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := h.waitForState(hash, "pausedUP", 30*time.Second); err != nil {
		t.Fatal(err)
	}

	var hasIntermediate bool
	for _, p := range progressSamples {
		if p > 0 && p < 1 {
			hasIntermediate = true
			break
		}
	}
	if !hasIntermediate {
		t.Errorf("no intermediate progress sample observed; all samples = %v\n"+
			"hint: payload must be >256 KB for two chunks — current payloadSize = 300 KB",
			progressSamples)
	}
}

// TestProviderError_Download: Xtream returns 403 for XtreamID 999.
// The download never completes within the assertion window.
//
// Known goroutine behaviour: the download manager goroutine continues running
// after this test returns — it waits out the 60 s auto-pause, then retries
// against the closed mock server and eventually calls SetFailed. This is
// cosmetic and does not affect other tests (isolated store and servers).
// Run with -count=1 to avoid accumulation.
func TestProviderError_Download(t *testing.T) {
	h := newHarness(t, "download")

	// Banned Film has XtreamID=999 — mock Xtream returns 403 for paths containing "/999."
	downloadURL := h.newznabSrv.URL + "/api?t=get&id=999&type=movie"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-movies")

	// Should NOT reach pausedUP — download blocked by 403 → auto-pause (60 s)
	if err := h.waitForState(hash, "pausedUP", 3*time.Second); err == nil {
		t.Error("expected waitForState to time out (provider 403 should block completion)")
	}

	// No video file should exist on disk
	absPath := h.writer.MovieFilePath("Banned Film", "", "mkv")
	rel := h.absToRel(absPath)
	if h.fileExists(rel) {
		t.Errorf("file should not exist after 403 from provider: %s", rel)
	}
}
