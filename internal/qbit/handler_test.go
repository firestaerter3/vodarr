package qbit

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/bencode"
)

// makeQbitHandler builds a Handler with an empty store and no auth.
// The writer, xtream, and prober fields are nil; only call endpoints that don't need them.
func makeQbitHandler(savePath string) *Handler {
	store := NewStore()
	return NewHandler(store, nil, nil, nil, savePath, "", "", "", "strm", nil)
}

func TestTorrentsFilesRelativePaths(t *testing.T) {
	savePath := "/data/strm"
	h := makeQbitHandler(savePath)
	h.store.Add(&Torrent{
		Hash:     "abc123",
		Name:     "Breaking Bad",
		SavePath: savePath,
		State:    StateDownloading,
		StrmPaths: []string{
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.strm",
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E02.strm",
		},
		MkvPaths: []string{
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.mkv",
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E02.mkv",
		},
	})

	req := httptest.NewRequest("GET", "/api/v2/torrents/files?hash=abc123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var files []struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 file entries, got %d", len(files))
	}
	for _, f := range files {
		if filepath.IsAbs(f.Name) {
			t.Errorf("Name is absolute: %q — Sonarr expects a path relative to save_path", f.Name)
		}
	}
	// MkvPaths take precedence — Sonarr must see .mkv so its extension filter passes.
	want0 := "tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.mkv"
	if files[0].Name != want0 {
		t.Errorf("files[0].Name = %q, want %q", files[0].Name, want0)
	}
}

func TestTorrentsInfoAmountLeft(t *testing.T) {
	savePath := "/data/strm"
	h := makeQbitHandler(savePath)
	h.store.Add(&Torrent{
		Hash:     "h1",
		Name:     "Test Movie",
		SavePath: savePath,
		State:    StateDownloading,
		Progress: 0,
		Size:     1024,
	})

	type qbEntry struct {
		AmountLeft int64   `json:"amount_left"`
		Progress   float64 `json:"progress"`
		State      string  `json:"state"`
	}
	doInfo := func() []qbEntry {
		req := httptest.NewRequest("GET", "/api/v2/torrents/info", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var entries []qbEntry
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return entries
	}

	// While downloading: amount_left must equal size so Sonarr waits.
	entries := doInfo()
	if len(entries) != 1 {
		t.Fatalf("expected 1 torrent, got %d", len(entries))
	}
	if entries[0].AmountLeft != 1024 {
		t.Errorf("amount_left (downloading) = %d, want 1024", entries[0].AmountLeft)
	}
	if entries[0].State != string(StateDownloading) {
		t.Errorf("state = %q, want %q", entries[0].State, StateDownloading)
	}

	// After STRM is written: amount_left must be 0 so Sonarr triggers import.
	h.store.SetComplete("h1",
		[]string{savePath + "/movies/Test.Movie.strm"},
		[]string{savePath + "/movies/Test.Movie.mkv"})
	entries = doInfo()
	if entries[0].AmountLeft != 0 {
		t.Errorf("amount_left (completed) = %d, want 0", entries[0].AmountLeft)
	}
	if entries[0].State != string(StatePausedUP) {
		t.Errorf("state (completed) = %q, want %q", entries[0].State, StatePausedUP)
	}
}

func TestSessionTTLEviction(t *testing.T) {
	h := makeQbitHandler("/data")
	h.username = "u"
	h.password = "p"

	// Manually insert an expired session
	expiredSID := "deadbeef"
	h.mu.Lock()
	h.sessions[expiredSID] = time.Now().Add(-25 * time.Hour)
	h.mu.Unlock()

	// Auth with expired SID should be rejected
	req := httptest.NewRequest("GET", "/api/v2/app/version", nil)
	req.AddCookie(&http.Cookie{Name: "SID", Value: expiredSID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expired session: status = %d, want 403", w.Code)
	}

	// Expired session must be evicted from map
	h.mu.RLock()
	_, stillExists := h.sessions[expiredSID]
	h.mu.RUnlock()
	if stillExists {
		t.Error("expired session was not evicted from map")
	}
}

func TestTorrentsAddTorrentFile(t *testing.T) {
	// Build a minimal .torrent that matches what handleGet produces.
	descriptor := map[string]interface{}{
		"xtream_id":     float64(42), // JSON numbers decode as float64
		"type":          "movie",
		"name":          "Test Movie",
		"year":          "2023",
		"imdb_id":       "tt9999999",
		"tvdb_id":       "",
		"tmdb_id":       "",
		"container_ext": "mkv",
		"episodes":      nil,
	}
	descJSON, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	pieces := string(make([]byte, 20))
	infoDict := map[string]interface{}{
		"length":       1,
		"name":         "vodarr-movie-42",
		"piece length": 262144,
		"pieces":       pieces,
	}
	torrent := map[string]interface{}{
		"comment": string(descJSON),
		"info":    infoDict,
	}
	torrentBytes, err := bencode.Encode(torrent)
	if err != nil {
		t.Fatalf("bencode.Encode: %v", err)
	}

	// Build multipart form with the .torrent file in the "torrents" field.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("torrents", "vodarr-movie-42.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(torrentBytes)
	mw.Close()

	h := makeQbitHandler("/data/strm")
	req := httptest.NewRequest("POST", "/api/v2/torrents/add", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "Ok." {
		t.Errorf("body = %q, want Ok.", w.Body.String())
	}

	// Torrent must appear in the store with the correct SHA1 info hash.
	torrents := h.store.All()
	if len(torrents) != 1 {
		t.Fatalf("store has %d torrents, want 1", len(torrents))
	}

	// Verify the hash is a 40-character hex string (SHA1)
	hash := torrents[0].Hash
	if len(hash) != 40 {
		t.Errorf("hash = %q (len %d), want 40-char SHA1 hex", hash, len(hash))
	}
	if torrents[0].Name != "Test Movie" {
		t.Errorf("Name = %q, want Test Movie", torrents[0].Name)
	}
}

func TestTorrentsFilesEmptyFallback(t *testing.T) {
	// Before files are written, torrents/files should return a fallback placeholder
	// with a relative .mkv name (so Sonarr's extension filter passes even before
	// the actual files exist on disk).
	h := makeQbitHandler("/data/strm")
	h.store.Add(&Torrent{
		Hash:     "pending",
		Name:     "Pending Movie",
		SavePath: "/data/strm",
		State:    StateDownloading,
	})

	req := httptest.NewRequest("GET", "/api/v2/torrents/files?hash=pending", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var files []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(w.Body.Bytes(), &files)
	if len(files) == 0 {
		t.Fatal("expected fallback file entry before strm is written")
	}
	if filepath.IsAbs(files[0].Name) {
		t.Errorf("fallback Name is absolute: %q", files[0].Name)
	}
	// Fallback must use .mkv so Sonarr's video-extension filter passes.
	if !strings.HasSuffix(files[0].Name, ".mkv") {
		t.Errorf("fallback Name = %q, want .mkv suffix", files[0].Name)
	}
}
