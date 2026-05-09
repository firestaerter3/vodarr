# E2E Test Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement 11 hermetic E2E tests covering both STRM and download output modes across all Sonarr/Radarr grab paths.

**Architecture:** Three `_test.go` files in `internal/e2e/` (package `e2e_test`). A shared harness spins up real in-process `httptest.Server` instances for Newznab, qBit, Web, and a mock Xtream provider. Tests poll `torrents/info` for async completion instead of injecting hooks.

**Tech Stack:** `net/http/httptest`, `encoding/xml`, `mime/multipart`, standard Go testing — no new dependencies.

---

## File Structure

| File | Role |
|------|------|
| `internal/e2e/harness_test.go` | Harness struct, `newHarness`, `newHarnessWithAuth`, and all HTTP helper methods |
| `internal/e2e/strm_test.go` | 7 STRM output mode tests |
| `internal/e2e/download_test.go` | 4 download output mode tests |

**No production files are created or modified.**

---

### Task 1: Harness (`internal/e2e/harness_test.go`)

**Goal:** Compile-ready harness with all 3 test servers and every helper the tests will call.

**Files:**
- Create: `internal/e2e/harness_test.go`

**Acceptance Criteria:**
- [ ] `go build ./internal/e2e/` (or `go test -run '^$' ./internal/e2e/`) compiles without error
- [ ] `go vet ./internal/e2e/` reports no issues
- [ ] All helper methods are present and have the correct signatures

**Verify:** `go test -run '^$' -count=1 ./internal/e2e/`  → `ok` (no test selected, package compiles)

**Steps:**

- [ ] **Step 1: Create `internal/e2e/harness_test.go` with the full harness**

```go
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/download"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/newznab"
	"github.com/vodarr/vodarr/internal/probe"
	"github.com/vodarr/vodarr/internal/qbit"
	"github.com/vodarr/vodarr/internal/strm"
	"github.com/vodarr/vodarr/internal/web"
	"github.com/vodarr/vodarr/internal/xtream"
)

// nilProber satisfies qbit.Prober but always returns nil.
// MKV stubs in tests use the default 500 MB sparse size.
type nilProber struct{}

func (nilProber) Probe(_ context.Context, _ string) (*probe.MediaInfo, error) {
	return nil, nil
}

type harness struct {
	t          *testing.T
	tmpDir     string
	xtreamSrv  *httptest.Server
	newznabSrv *httptest.Server
	qbitSrv    *httptest.Server
	webSrv     *httptest.Server
	writer     *strm.Writer
	mkvPayload []byte // served by mock Xtream; expected download size
}

// newHarness is the zero-auth convenience wrapper.
func newHarness(t *testing.T, outputMode string) *harness {
	t.Helper()
	return newHarnessWithAuth(t, outputMode, "", "")
}

func newHarnessWithAuth(t *testing.T, outputMode, qbitUsername, qbitPassword string) *harness {
	t.Helper()
	tmpDir := t.TempDir()

	// 1. Index fixtures ----------------------------------------------------
	idx := index.New()
	idx.Replace([]*index.Item{
		{
			XtreamID:     1,
			Name:         "The Matrix",
			Year:         "1999",
			IMDBId:       "tt0133093",
			TMDBId:       "603",
			Type:         index.TypeMovie,
			ContainerExt: "mkv",
		},
		{
			XtreamID: 2,
			Name:     "Breaking Bad",
			Year:     "2008",
			TVDBId:   "81189",
			TMDBId:   "1396",
			Type:     index.TypeSeries,
			Episodes: []index.EpisodeItem{
				{EpisodeID: 101, Season: 1, EpisodeNum: 1, Title: "Pilot", Ext: "mkv"},
				{EpisodeID: 102, Season: 1, EpisodeNum: 2, Title: "Cat's in the Bag", Ext: "mkv"},
				{EpisodeID: 201, Season: 2, EpisodeNum: 1, Title: "Seven Thirty-Seven", Ext: "mkv"},
			},
		},
		{
			XtreamID:     3,
			Name:         "Unknown Film",
			Type:         index.TypeMovie,
			ContainerExt: "mkv",
		},
		{
			XtreamID:     999,
			Name:         "Banned Film",
			IMDBId:       "tt9999999",
			Type:         index.TypeMovie,
			ContainerExt: "mkv",
		},
	})

	// 2. Payload: MKV header padded to 300 KB ----------------------------
	// 300 KB > 256 KB chunk size → download manager produces at least one
	// intermediate progress update, required by TestProgressTracking_Download.
	const payloadSize = 300 * 1024
	mkvPayload := make([]byte, payloadSize)
	copy(mkvPayload, strm.BuildMKVHeader(nil))

	// 3. Mock Xtream server -----------------------------------------------
	xtreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/999.") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/movie/") || strings.HasPrefix(r.URL.Path, "/series/") {
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(mkvPayload)))
			w.WriteHeader(http.StatusOK)
			w.Write(mkvPayload) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(xtreamSrv.Close)

	// 4. Xtream client ---------------------------------------------------
	// Credentials "user"/"pass" are path-escaped into stream URLs:
	// {xtreamSrv.URL}/movie/user/pass/{id}.mkv
	xc := xtream.NewClient(xtreamSrv.URL, "user", "pass")

	// 5. STRM writer -----------------------------------------------------
	writer := strm.NewWriter(tmpDir, "movies", "tv")

	// 6. Download manager -----------------------------------------------
	// InterDelay must NOT be 0 — NewManager defaults 0 → 30s.
	dlManager := download.NewManager(download.Options{
		MaxConcurrent: 1,
		InterDelay:    1 * time.Millisecond,
	})

	// 7. Newznab server — deferred-assignment pattern -------------------
	// newznab.NewHandler needs its own server URL (to build t=get links),
	// but httptest.NewServer gives us the URL only after the server is started.
	// We break the cycle by wrapping in a closure that delegates to a ref
	// assigned after the server is created.
	var newznabHandlerRef http.Handler
	newznabSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newznabHandlerRef.ServeHTTP(w, r)
	}))
	t.Cleanup(newznabSrv.Close)

	newznabH := newznab.NewHandler(idx, "", newznabSrv.URL, xc)
	mux := http.NewServeMux()
	mux.Handle("/api", newznabH)
	mux.Handle("/api/", newznabH)
	newznabHandlerRef = mux

	// 8. qBit server ------------------------------------------------------
	// newznabSrv.URL is passed as newznabURL so the SSRF host check passes
	// when the handler fetches descriptor URLs built by the Newznab handler.
	store := qbit.NewStore()
	qbitH := qbit.NewHandler(
		store, writer, xc, nilProber{},
		tmpDir, qbitUsername, qbitPassword,
		newznabSrv.URL, outputMode, dlManager,
	)
	qbitSrv := httptest.NewServer(qbitH)
	t.Cleanup(qbitSrv.Close)

	// 9. Web server (webhook tests) ---------------------------------------
	cfg := &config.Config{Output: config.OutputConfig{Path: tmpDir}}
	webH := web.NewHandler(idx, nil, writer, nil, cfg, "", "", "", "test")
	webSrv := httptest.NewServer(webH)
	t.Cleanup(webSrv.Close)

	return &harness{
		t:          t,
		tmpDir:     tmpDir,
		xtreamSrv:  xtreamSrv,
		newznabSrv: newznabSrv,
		qbitSrv:    qbitSrv,
		webSrv:     webSrv,
		writer:     writer,
		mkvPayload: mkvPayload,
	}
}

// ---- Search helpers -------------------------------------------------------

func (h *harness) searchMovie(imdbID string) string {
	h.t.Helper()
	resp, err := http.Get(h.newznabSrv.URL + "/api?t=movie&imdbid=" + imdbID)
	if err != nil {
		h.t.Fatalf("searchMovie: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func (h *harness) searchSeries(tvdbID string) string {
	h.t.Helper()
	resp, err := http.Get(h.newznabSrv.URL + "/api?t=tvsearch&tvdbid=" + tvdbID)
	if err != nil {
		h.t.Fatalf("searchSeries: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func (h *harness) searchText(q string) string {
	h.t.Helper()
	resp, err := http.Get(h.newznabSrv.URL + "/api?t=search&q=" + url.QueryEscape(q))
	if err != nil {
		h.t.Fatalf("searchText: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// extractDownloadURLs parses Newznab RSS XML and returns all enclosure URLs.
func (h *harness) extractDownloadURLs(rssXML string) []string {
	h.t.Helper()
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Items   []struct {
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal([]byte(rssXML), &feed); err != nil {
		h.t.Fatalf("extractDownloadURLs: %v", err)
	}
	var out []string
	for _, item := range feed.Items {
		if item.Enclosure.URL != "" {
			out = append(out, item.Enclosure.URL)
		}
	}
	return out
}

// fetchTorrent fetches a t=get URL and returns the raw bencode .torrent bytes.
func (h *harness) fetchTorrent(downloadURL string) []byte {
	h.t.Helper()
	resp, err := http.Get(downloadURL)
	if err != nil {
		h.t.Fatalf("fetchTorrent: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("fetchTorrent read: %v", err)
	}
	return data
}

// ---- Grab helpers ---------------------------------------------------------

// grabByURL POSTs a Newznab descriptor URL to qBit torrents/add and returns
// the hash of the newly added torrent. The qBit handler adds the torrent to
// the store synchronously before launching the async goroutine, so the hash
// is immediately available.
func (h *harness) grabByURL(downloadURL, savePath, category string) string {
	h.t.Helper()
	form := url.Values{
		"urls":     {downloadURL},
		"savepath": {savePath},
		"category": {category},
	}
	resp, err := http.PostForm(h.qbitSrv.URL+"/api/v2/torrents/add", form)
	if err != nil {
		h.t.Fatalf("grabByURL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("grabByURL: status %d body %q", resp.StatusCode, b)
	}
	return h.latestHash()
}

// grabByTorrent uploads a raw .torrent file via multipart/form-data and
// returns the hash of the newly added torrent.
func (h *harness) grabByTorrent(torrentBytes []byte, savePath, category string) string {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("savepath", savePath); err != nil {
		h.t.Fatalf("grabByTorrent: write savepath: %v", err)
	}
	if err := mw.WriteField("category", category); err != nil {
		h.t.Fatalf("grabByTorrent: write category: %v", err)
	}
	fw, err := mw.CreateFormFile("torrents", "file.torrent")
	if err != nil {
		h.t.Fatalf("grabByTorrent: create form file: %v", err)
	}
	if _, err := fw.Write(torrentBytes); err != nil {
		h.t.Fatalf("grabByTorrent: write torrent bytes: %v", err)
	}
	mw.Close()

	req, err := http.NewRequest("POST", h.qbitSrv.URL+"/api/v2/torrents/add", &body)
	if err != nil {
		h.t.Fatalf("grabByTorrent: new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("grabByTorrent: do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("grabByTorrent: status %d body %q", resp.StatusCode, b)
	}
	return h.latestHash()
}

// latestHash returns the hash of the first torrent in the store.
// Safe because each test creates a fresh harness with an empty store.
func (h *harness) latestHash() string {
	h.t.Helper()
	resp, err := http.Get(h.qbitSrv.URL + "/api/v2/torrents/info")
	if err != nil {
		h.t.Fatalf("latestHash: %v", err)
	}
	defer resp.Body.Close()
	var torrents []struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		h.t.Fatalf("latestHash decode: %v", err)
	}
	if len(torrents) == 0 {
		h.t.Fatal("latestHash: no torrents in store")
	}
	return torrents[0].Hash
}

// ---- State polling helpers ------------------------------------------------

// waitForState polls torrents/info every 10 ms until the torrent reaches
// target state or timeout elapses.
func (h *harness) waitForState(hash, target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.qbitSrv.URL + "/api/v2/torrents/info?hashes=" + hash)
		if err != nil {
			return err
		}
		var torrents []struct {
			State string `json:"state"`
		}
		json.NewDecoder(resp.Body).Decode(&torrents) //nolint:errcheck
		resp.Body.Close()
		if len(torrents) > 0 && torrents[0].State == target {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("hash %s did not reach state %q within %s", hash, target, timeout)
}

// torrentFiles returns the list of file names reported by torrents/files for hash.
// Names are relative to the torrent's save_path (as Sonarr/Radarr expect).
func (h *harness) torrentFiles(hash string) []string {
	h.t.Helper()
	resp, err := http.Get(h.qbitSrv.URL + "/api/v2/torrents/files?hash=" + hash)
	if err != nil {
		h.t.Fatalf("torrentFiles: %v", err)
	}
	defer resp.Body.Close()
	var files []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		h.t.Fatalf("torrentFiles decode: %v", err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return names
}

// ---- Filesystem helpers ---------------------------------------------------

// fileExists reports whether relPath (relative to harness tmpDir) exists.
func (h *harness) fileExists(relPath string) bool {
	_, err := os.Stat(filepath.Join(h.tmpDir, relPath))
	return err == nil
}

// fileContent reads and returns the contents of relPath relative to tmpDir.
func (h *harness) fileContent(relPath string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.tmpDir, relPath))
	if err != nil {
		h.t.Fatalf("fileContent %q: %v", relPath, err)
	}
	return string(data)
}

// fileSize returns the byte size of relPath relative to tmpDir.
func (h *harness) fileSize(relPath string) int64 {
	h.t.Helper()
	fi, err := os.Stat(filepath.Join(h.tmpDir, relPath))
	if err != nil {
		h.t.Fatalf("fileSize %q: %v", relPath, err)
	}
	return fi.Size()
}

// absToRel converts an absolute file path under h.tmpDir to a relative path.
// Useful when writer.MovieFilePath / writer.EpisodeFilePath return absolute paths
// and the harness file helpers expect relative paths.
func (h *harness) absToRel(absPath string) string {
	h.t.Helper()
	rel, err := filepath.Rel(h.tmpDir, absPath)
	if err != nil {
		h.t.Fatalf("absToRel %q: %v", absPath, err)
	}
	return rel
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/rolandbo@backbase.com/Documents/Coding\ Projects/vodarr && \
  go test -run '^$' -count=1 ./internal/e2e/
```

Expected: `ok  	github.com/vodarr/vodarr/internal/e2e [no test files]`  
If the package has no `_test.go` with test functions, Go may say "no test files" — that's fine; the important thing is no compile error.

- [ ] **Step 3: Commit**

```bash
git add internal/e2e/harness_test.go
git commit -m "test(e2e): add shared harness, mock servers, and helpers"
```

---

### Task 2: STRM Mode Tests (`internal/e2e/strm_test.go`)

**Goal:** 7 passing tests that verify all STRM output paths — URL grab, torrent upload, series, single episode, unenriched item, auth, and webhook cleanup.

**Files:**
- Create: `internal/e2e/strm_test.go`
- Depends on: Task 1

**Acceptance Criteria:**
- [ ] All 7 tests pass: `go test -v -run 'TestMovie|TestSeries|TestUnenriched|TestMovieGrab_Auth|TestWebhook' ./internal/e2e/`
- [ ] No test takes more than 2 s wall-clock time

**Verify:** `go test -v -count=1 -timeout 30s ./internal/e2e/ -run 'Strm'`  → 7 PASS lines

**Steps:**

- [ ] **Step 1: Write one failing test to confirm harness wires up correctly**

Create `internal/e2e/strm_test.go` with only `TestMovieGrab_URL_Strm` and run it:

```bash
go test -v -run TestMovieGrab_URL_Strm -count=1 ./internal/e2e/
```

Expected: PASS (if it fails, fix the harness wiring before adding more tests).

- [ ] **Step 2: Write all 7 tests**

```go
package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMovieGrab_URL_Strm: full movie flow via URL-based Newznab grab.
//
// File paths produced by strm.Writer with name="The Matrix", year="1999", ext="mkv":
//   movies/The Matrix (1999)/The.Matrix.1999.WEB-DL.strm
//   movies/The Matrix (1999)/The.Matrix.1999.WEB-DL.mkv
func TestMovieGrab_URL_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	rss := h.searchMovie("tt0133093")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no download URLs in movie search RSS")
	}

	hash := h.grabByURL(urls[0], h.tmpDir, "vodarr-movies")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	files := h.torrentFiles(hash)
	if len(files) != 1 {
		t.Fatalf("expected 1 file entry, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], ".mkv") {
		t.Errorf("torrent file should be .mkv, got %q", files[0])
	}

	strmRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found at %s", strmRel)
	}
	content := h.fileContent(strmRel)
	if !strings.HasPrefix(strings.TrimSpace(content), h.xtreamSrv.URL+"/movie/") {
		t.Errorf(".strm content = %q, want prefix %s/movie/", content, h.xtreamSrv.URL)
	}

	mkvRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.mkv")
	if !h.fileExists(mkvRel) {
		t.Errorf(".mkv stub not found at %s", mkvRel)
	}
}

// TestMovieGrab_Torrent_Strm: same movie via torrent file upload (Torznab path).
func TestMovieGrab_Torrent_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	rss := h.searchMovie("tt0133093")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no download URLs in movie search RSS")
	}

	torrentBytes := h.fetchTorrent(urls[0])
	hash := h.grabByTorrent(torrentBytes, h.tmpDir, "vodarr-movies")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	strmRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found at %s (torrent grab path)", strmRel)
	}
	content := h.fileContent(strmRel)
	if !strings.HasPrefix(strings.TrimSpace(content), h.xtreamSrv.URL+"/movie/") {
		t.Errorf(".strm content %q does not point to Xtream server", content)
	}
	mkvRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.mkv")
	if !h.fileExists(mkvRel) {
		t.Errorf(".mkv stub not found at %s (torrent grab path)", mkvRel)
	}
}

// TestSeriesGrab_URL_Strm: grab all 3 Breaking Bad episodes in one request.
//
// A series-level URL (no episode_id) returns a descriptor with all episodes.
// fileSafe("Cat's in the Bag") = "Cat's.in.the.Bag" (apostrophe is kept;
// illegalChars regex is [<>:"/\|?*] — apostrophe not in set).
func TestSeriesGrab_URL_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	// Series-level grab: no episode_id → all 3 episodes in descriptor
	downloadURL := h.newznabSrv.URL + "/api?t=get&id=2&type=series"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-tv")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	files := h.torrentFiles(hash)
	if len(files) != 3 {
		t.Fatalf("expected 3 file entries (one per episode), got %d: %v", len(files), files)
	}

	wantStrm := []string{
		filepath.Join("tv", "Breaking Bad", "Season 01", "Breaking.Bad.S01E01.Pilot.WEB-DL.strm"),
		filepath.Join("tv", "Breaking Bad", "Season 01", "Breaking.Bad.S01E02.Cat's.in.the.Bag.WEB-DL.strm"),
		filepath.Join("tv", "Breaking Bad", "Season 02", "Breaking.Bad.S02E01.Seven.Thirty-Seven.WEB-DL.strm"),
	}
	for _, rel := range wantStrm {
		if !h.fileExists(rel) {
			t.Errorf(".strm not found: %s", rel)
			continue
		}
		if !strings.Contains(h.fileContent(rel), "/series/") {
			t.Errorf(".strm at %s does not contain /series/ in URL", rel)
		}
	}
}

// TestSeriesGrab_SingleEpisode_Strm: episode_id in URL filters to exactly one episode.
func TestSeriesGrab_SingleEpisode_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	// episode_id=101 → S01E01 Pilot only
	downloadURL := h.newznabSrv.URL + "/api?t=get&id=2&type=series&episode_id=101"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-tv")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	files := h.torrentFiles(hash)
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 file for single-episode grab, got %d: %v", len(files), files)
	}

	s01e01 := filepath.Join("tv", "Breaking Bad", "Season 01", "Breaking.Bad.S01E01.Pilot.WEB-DL.strm")
	if !h.fileExists(s01e01) {
		t.Errorf("S01E01 .strm not found: %s", s01e01)
	}

	// Other episodes must NOT be created
	s01e02 := filepath.Join("tv", "Breaking Bad", "Season 01", "Breaking.Bad.S01E02.Cat's.in.the.Bag.WEB-DL.strm")
	if h.fileExists(s01e02) {
		t.Errorf("S01E02 .strm exists but should not (single episode grab)")
	}
	s02e01 := filepath.Join("tv", "Breaking Bad", "Season 02", "Breaking.Bad.S02E01.Seven.Thirty-Seven.WEB-DL.strm")
	if h.fileExists(s02e01) {
		t.Errorf("S02E01 .strm exists but should not (single episode grab)")
	}
}

// TestUnenrichedMovie_Strm: item with no IMDB/TVDB/TMDB IDs is still reachable
// via text search and produces a .strm file derived from the raw Xtream title.
func TestUnenrichedMovie_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	rss := h.searchText("Unknown Film")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no results for 'Unknown Film' text search")
	}

	hash := h.grabByURL(urls[0], h.tmpDir, "vodarr-movies")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	// No year → folder is "Unknown Film" (no parenthesised year suffix)
	strmRel := filepath.Join("movies", "Unknown Film", "Unknown.Film.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found at %s (unenriched movie)", strmRel)
	}
}

// TestMovieGrab_AuthRequired_Strm: unauthenticated grab returns 403;
// authenticated grab (SID cookie from auth/login) succeeds.
func TestMovieGrab_AuthRequired_Strm(t *testing.T) {
	h := newHarnessWithAuth(t, "strm", "admin", "secret")

	rss := h.searchMovie("tt0133093")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no download URLs in movie search RSS")
	}
	downloadURL := urls[0]

	// 1. Unauthenticated → 403
	form := url.Values{"urls": {downloadURL}, "savepath": {h.tmpDir}, "category": {"vodarr-movies"}}
	unauthResp, err := http.PostForm(h.qbitSrv.URL+"/api/v2/torrents/add", form)
	if err != nil {
		t.Fatalf("unauthenticated grab: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusForbidden {
		t.Errorf("unauthenticated grab: got %d, want 403", unauthResp.StatusCode)
	}

	// 2. Login → SID cookie
	loginForm := url.Values{"username": {"admin"}, "password": {"secret"}}
	loginResp, err := http.PostForm(h.qbitSrv.URL+"/api/v2/auth/login", loginForm)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	loginResp.Body.Close()
	var sid string
	for _, c := range loginResp.Cookies() {
		if c.Name == "SID" {
			sid = c.Value
			break
		}
	}
	if sid == "" {
		t.Fatal("login response missing SID cookie")
	}

	// 3. Authenticated grab → "Ok."
	authReq, _ := http.NewRequest("POST", h.qbitSrv.URL+"/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	authReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	authReq.AddCookie(&http.Cookie{Name: "SID", Value: sid})
	authResp, err := http.DefaultClient.Do(authReq)
	if err != nil {
		t.Fatalf("authenticated grab: %v", err)
	}
	body, _ := io.ReadAll(authResp.Body)
	authResp.Body.Close()
	if !strings.Contains(string(body), "Ok.") {
		t.Fatalf("authenticated grab: got %q, want 'Ok.'", body)
	}

	// 4. Poll torrents/info with SID cookie
	deadline := time.Now().Add(2 * time.Second)
	var hash string
	for time.Now().Before(deadline) {
		infoReq, _ := http.NewRequest("GET", h.qbitSrv.URL+"/api/v2/torrents/info", nil)
		infoReq.AddCookie(&http.Cookie{Name: "SID", Value: sid})
		infoResp, err := http.DefaultClient.Do(infoReq)
		if err != nil {
			t.Fatalf("torrents/info: %v", err)
		}
		var torrents []struct {
			Hash  string `json:"hash"`
			State string `json:"state"`
		}
		json.NewDecoder(infoResp.Body).Decode(&torrents) //nolint:errcheck
		infoResp.Body.Close()
		if len(torrents) > 0 {
			hash = torrents[0].Hash
			if torrents[0].State == "pausedUP" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hash == "" {
		t.Fatal("no torrent in store after authenticated grab")
	}

	strmRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found after authenticated grab: %s", strmRel)
	}
}

// TestWebhookStubCleanup_Strm: POST /api/webhook with MovieFileImported event
// deletes the .mkv stub and preserves the .strm file.
func TestWebhookStubCleanup_Strm(t *testing.T) {
	h := newHarness(t, "strm")

	// 1. Full movie grab
	rss := h.searchMovie("tt0133093")
	urls := h.extractDownloadURLs(rss)
	if len(urls) == 0 {
		t.Fatal("no download URLs")
	}
	hash := h.grabByURL(urls[0], h.tmpDir, "vodarr-movies")
	if err := h.waitForState(hash, "pausedUP", 2*time.Second); err != nil {
		t.Fatal(err)
	}

	strmRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.strm")
	mkvRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.mkv")
	if !h.fileExists(strmRel) {
		t.Fatalf(".strm not found before webhook: %s", strmRel)
	}
	if !h.fileExists(mkvRel) {
		t.Fatalf(".mkv not found before webhook: %s", mkvRel)
	}
	strmContentBefore := h.fileContent(strmRel)

	// 2. POST webhook with absolute .mkv path
	// The web handler validates path is inside cfg.Output.Path (our tmpDir).
	mkvAbsPath := filepath.Join(h.tmpDir, mkvRel)
	payload, _ := json.Marshal(map[string]interface{}{
		"eventType": "Download",
		"movieFile": map[string]string{"path": mkvAbsPath},
	})
	resp, err := http.Post(h.webSrv.URL+"/api/webhook", "application/json",
		bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("webhook POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("webhook status %d, want 200", resp.StatusCode)
	}

	// 3. .mkv deleted, .strm preserved and unchanged
	if h.fileExists(mkvRel) {
		t.Errorf(".mkv still on disk after webhook: %s", mkvRel)
	}
	if !h.fileExists(strmRel) {
		t.Errorf(".strm was deleted (must be preserved): %s", strmRel)
	}
	if h.fileContent(strmRel) != strmContentBefore {
		t.Errorf(".strm content changed after webhook")
	}
}
```

- [ ] **Step 3: Run all STRM tests**

```bash
cd /Users/rolandbo@backbase.com/Documents/Coding\ Projects/vodarr && \
  go test -v -count=1 -timeout 30s ./internal/e2e/ -run 'Strm|TestMovieGrab_AuthRequired|TestWebhookStub'
```

Expected output (7 lines):
```
--- PASS: TestMovieGrab_URL_Strm (0.00s)
--- PASS: TestMovieGrab_Torrent_Strm (0.00s)
--- PASS: TestSeriesGrab_URL_Strm (0.00s)
--- PASS: TestSeriesGrab_SingleEpisode_Strm (0.00s)
--- PASS: TestUnenrichedMovie_Strm (0.00s)
--- PASS: TestMovieGrab_AuthRequired_Strm (0.00s)
--- PASS: TestWebhookStubCleanup_Strm (0.00s)
```

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/strm_test.go
git commit -m "test(e2e): add 7 STRM mode E2E tests"
```

---

### Task 3: Download Mode Tests (`internal/e2e/download_test.go`)

**Goal:** 4 passing tests covering full download flow, series multi-episode, progress tracking, and provider 403 error handling.

**Files:**
- Create: `internal/e2e/download_test.go`
- Depends on: Task 1

**Acceptance Criteria:**
- [ ] All 4 tests pass: `go test -v -run 'Download' ./internal/e2e/`
- [ ] `TestMovieGrab_URL_Download` and `TestSeriesGrab_URL_Download` verify byte-exact file sizes against `len(h.mkvPayload)`
- [ ] `TestProgressTracking_Download` observes at least one progress sample where `0 < p < 1.0`
- [ ] `TestProviderError_Download` correctly times out (download never completes within 3 s)

**Verify:** `go test -v -count=1 -timeout 60s ./internal/e2e/ -run 'Download'`

**Note on `TestProviderError_Download`:** The download manager retries with exponential backoff and a 60 s auto-pause after 403. The goroutine continues running after the test function returns (goroutine leak). This is cosmetic — the goroutine never writes any file and does not interfere with other tests (each test uses an isolated `httptest.Server` and `qbit.Store`). Run `go test -count=1` (not `-count=N`) to avoid accumulation across multiple runs.

**Steps:**

- [ ] **Step 1: Write all 4 download tests**

```go
package e2e_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
// The harness uses a 300 KB payload and the download manager uses 256 KB chunks,
// so the first chunk yields progress ≈ 256/300 ≈ 0.85 — an observable intermediate value.
func TestProgressTracking_Download(t *testing.T) {
	h := newHarness(t, "download")

	downloadURL := h.newznabSrv.URL + "/api?t=get&id=1&type=movie"
	hash := h.grabByURL(downloadURL, h.tmpDir, "vodarr-movies")

	// Collect progress samples until done or timeout
	var progressSamples []float64
	deadline := time.Now().Add(5 * time.Second)
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
		time.Sleep(5 * time.Millisecond)
	}

	if err := h.waitForState(hash, "pausedUP", 5*time.Second); err != nil {
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
```

- [ ] **Step 2: Run all download tests**

```bash
cd /Users/rolandbo@backbase.com/Documents/Coding\ Projects/vodarr && \
  go test -v -count=1 -timeout 60s ./internal/e2e/ -run 'Download'
```

Expected output (4 lines):
```
--- PASS: TestMovieGrab_URL_Download (0.xx s)
--- PASS: TestSeriesGrab_URL_Download (0.xx s)
--- PASS: TestProgressTracking_Download (0.xx s)
--- PASS: TestProviderError_Download (3.0x s)
```

`TestProviderError_Download` takes ~3 s (the timeout window). All others should be under 1 s.

- [ ] **Step 3: Run the full E2E suite**

```bash
cd /Users/rolandbo@backbase.com/Documents/Coding\ Projects/vodarr && \
  go test -v -count=1 -timeout 60s ./internal/e2e/
```

Expected: 11 PASS lines, no FAIL.

- [ ] **Step 4: Run the full test suite to verify no regressions**

```bash
cd /Users/rolandbo@backbase.com/Documents/Coding\ Projects/vodarr && \
  go test -count=1 ./...
```

Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/e2e/download_test.go
git commit -m "test(e2e): add 4 download mode E2E tests"
```

---

## Self-Review

**Spec coverage check:**

| Spec test | Plan task | Covered? |
|-----------|-----------|----------|
| TestMovieGrab_URL_Strm | Task 2 | ✓ |
| TestMovieGrab_Torrent_Strm | Task 2 | ✓ |
| TestSeriesGrab_URL_Strm | Task 2 | ✓ |
| TestSeriesGrab_SingleEpisode_Strm | Task 2 | ✓ |
| TestUnenrichedMovie_Strm | Task 2 | ✓ |
| TestMovieGrab_AuthRequired_Strm | Task 2 | ✓ |
| TestWebhookStubCleanup_Strm | Task 2 | ✓ |
| TestMovieGrab_URL_Download | Task 3 | ✓ |
| TestSeriesGrab_URL_Download | Task 3 | ✓ |
| TestProgressTracking_Download | Task 3 | ✓ |
| TestProviderError_Download | Task 3 | ✓ |

**Key implementation decisions not obvious from spec:**

1. **300 KB payload** — BuildMKVHeader returns ~hundreds of bytes; the download manager uses 256 KB chunks; padded payload guarantees intermediate progress updates.
2. **Series-level URL** — `t=get&id=2&type=series` (no episode_id) returns all episodes. RSS items from `t=tvsearch` already have per-episode URLs with `episode_id` embedded; the series-level URL is constructed directly in the test.
3. **`absToRel` helper** — `writer.MovieFilePath` returns absolute paths; the harness file helpers take relative paths; `absToRel` bridges the gap.
4. **Auth test uses inline HTTP** — the `grabByURL` helper uses plain `http.PostForm` (no cookie support). Auth test performs requests directly to avoid per-helper auth plumbing.
5. **`InterDelay: 1 * time.Millisecond`** — `download.NewManager` with `InterDelay: 0` silently defaults to 30 s. Never use 0 in tests.
6. **Goroutine leak in error test** — documented in test comment and plan note. Cosmetic, not a correctness issue.
