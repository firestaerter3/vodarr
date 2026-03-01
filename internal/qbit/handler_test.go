package qbit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// makeQbitHandler builds a Handler with an empty store and no auth.
// The writer and xtream fields are nil; only call endpoints that don't need them.
func makeQbitHandler(savePath string) *Handler {
	store := NewStore()
	return NewHandler(store, nil, nil, savePath, "", "")
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
	want0 := "tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.strm"
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
	h.store.SetComplete("h1", []string{savePath + "/movies/Test.Movie.strm"})
	entries = doInfo()
	if entries[0].AmountLeft != 0 {
		t.Errorf("amount_left (completed) = %d, want 0", entries[0].AmountLeft)
	}
	if entries[0].State != string(StatePausedUP) {
		t.Errorf("state (completed) = %q, want %q", entries[0].State, StatePausedUP)
	}
}

func TestTorrentsFilesEmptyFallback(t *testing.T) {
	// Before STRM is written, torrents/files should return a fallback placeholder
	// with a relative name (no absolute path).
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
}
