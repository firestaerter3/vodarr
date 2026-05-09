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
//
//	movies/The Matrix (1999)/The.Matrix.1999.WEB-DL.strm
//	movies/The Matrix (1999)/The.Matrix.1999.WEB-DL.mkv
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

	files := h.torrentFiles(hash)
	if len(files) != 1 {
		t.Fatalf("expected 1 file entry, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], ".mkv") {
		t.Errorf("torrent file should be .mkv, got %q", files[0])
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
		if !strings.HasPrefix(strings.TrimSpace(h.fileContent(rel)), h.xtreamSrv.URL+"/series/") {
			t.Errorf(".strm at %s does not start with %s/series/", rel, h.xtreamSrv.URL)
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

	files := h.torrentFiles(hash)
	if len(files) != 1 {
		t.Fatalf("expected 1 file entry, got %d: %v", len(files), files)
	}

	// No year → folder is "Unknown Film" (no parenthesised year suffix)
	strmRel := filepath.Join("movies", "Unknown Film", "Unknown.Film.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found at %s (unenriched movie)", strmRel)
	}
	mkvRel := filepath.Join("movies", "Unknown Film", "Unknown.Film.WEB-DL.mkv")
	if !h.fileExists(mkvRel) {
		t.Errorf(".mkv stub not found at %s (unenriched movie)", mkvRel)
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
	authReq, err := http.NewRequest("POST", h.qbitSrv.URL+"/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build auth request: %v", err)
	}
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

	// 4. Poll torrents/info with SID cookie until pausedUP
	deadline := time.Now().Add(2 * time.Second)
	var hash, finalState string
	for time.Now().Before(deadline) {
		infoReq, err := http.NewRequest("GET", h.qbitSrv.URL+"/api/v2/torrents/info", nil)
		if err != nil {
			t.Fatalf("build info request: %v", err)
		}
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
			finalState = torrents[0].State
			if finalState == "pausedUP" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hash == "" {
		t.Fatal("no torrent in store after authenticated grab")
	}
	if finalState != "pausedUP" {
		t.Fatalf("torrent %s did not reach pausedUP within 2s (last state: %q)", hash, finalState)
	}

	strmRel := filepath.Join("movies", "The Matrix (1999)", "The.Matrix.1999.WEB-DL.strm")
	if !h.fileExists(strmRel) {
		t.Errorf(".strm not found after authenticated grab: %s", strmRel)
	}
}

// TestWebhookStubCleanup_Strm: POST /api/webhook with Download event
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
