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

	// 1. Index fixtures
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

	// 2. Payload: MKV header padded to 300 KB
	// 300 KB > 256 KB chunk size → download manager produces at least one
	// intermediate progress update, required by TestProgressTracking_Download.
	const payloadSize = 300 * 1024
	mkvPayload := make([]byte, payloadSize)
	copy(mkvPayload, strm.BuildMKVHeader(nil))

	// 3. Mock Xtream server
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

	// 4. Xtream client — credentials "user"/"pass" are path-escaped into stream URLs
	xc := xtream.NewClient(xtreamSrv.URL, "user", "pass")

	// 5. STRM writer
	writer := strm.NewWriter(tmpDir, "movies", "tv")

	// 6. Download manager — InterDelay must NOT be 0 (defaults to 30 s)
	dlManager := download.NewManager(download.Options{
		MaxConcurrent: 1,
		InterDelay:    1 * time.Millisecond,
	})

	// 7. Newznab server — deferred-assignment pattern
	// newznab.NewHandler needs its own server URL to build t=get links, but
	// httptest.NewServer gives us the URL only after the server starts.
	// We break the cycle with a closure that delegates to a ref assigned after.
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

	// 8. qBit server
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

	// 9. Web server (for webhook tests)
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
// Correct only when exactly one grab has been issued since the harness was
// created. Map iteration is non-deterministic — do not call after multiple grabs.
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
