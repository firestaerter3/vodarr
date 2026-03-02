package sync

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

func TestIndexCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-cache.json")

	items := []*index.Item{
		{
			Type:     index.TypeMovie,
			XtreamID: 1,
			Name:     "The Matrix",
			TMDBId:   "603",
			IMDBId:   "tt0133093",
			Year:     "1999",
		},
		{
			Type:     index.TypeSeries,
			XtreamID: 2,
			Name:     "Breaking Bad",
			TMDBId:   "1396",
			TVDBId:   "81189",
			Episodes: []index.EpisodeItem{
				{EpisodeID: 1, Season: 1, EpisodeNum: 1, Title: "Pilot", Ext: "mkv"},
			},
		},
	}

	if err := SaveIndexCache(path, items); err != nil {
		t.Fatalf("SaveIndexCache: %v", err)
	}

	loaded, err := LoadIndexCache(path)
	if err != nil {
		t.Fatalf("LoadIndexCache: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadIndexCache returned nil")
	}
	if len(loaded.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(loaded.Items))
	}

	m := loaded.Items[0]
	if m.Type != index.TypeMovie {
		t.Errorf("type = %q, want %q", m.Type, index.TypeMovie)
	}
	if m.Name != "The Matrix" {
		t.Errorf("name = %q, want %q", m.Name, "The Matrix")
	}
	if m.IMDBId != "tt0133093" {
		t.Errorf("imdb = %q, want %q", m.IMDBId, "tt0133093")
	}

	s := loaded.Items[1]
	if s.TVDBId != "81189" {
		t.Errorf("tvdb = %q, want %q", s.TVDBId, "81189")
	}
	if len(s.Episodes) != 1 {
		t.Errorf("episodes len = %d, want 1", len(s.Episodes))
	}
	if loaded.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestLoadIndexCacheMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	cache, err := LoadIndexCache(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cache != nil {
		t.Fatalf("expected nil cache for missing file, got %+v", cache)
	}
}

func TestLoadIndexCacheCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-cache.json")

	if err := os.WriteFile(path, []byte("not valid json{{{{"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cache, err := LoadIndexCache(path)
	if err == nil {
		t.Fatal("expected error for corrupt cache, got nil")
	}
	if cache != nil {
		t.Fatal("expected nil cache for corrupt file")
	}
}

func TestSaveIndexCacheAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-cache.json")

	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 99, Name: "Test Movie"},
	}

	if err := SaveIndexCache(path, items); err != nil {
		t.Fatalf("SaveIndexCache: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file in dir after save, got %d: %v", len(entries), entries)
	}
	if entries[0].Name() != ".vodarr-cache.json" {
		t.Errorf("unexpected file: %s", entries[0].Name())
	}
}

func TestCachePathHelper(t *testing.T) {
	got := CachePath("/data/output")
	want := "/data/output/.vodarr-cache.json"
	if got != want {
		t.Errorf("CachePath = %q, want %q", got, want)
	}
}

func TestSaveIndexCacheTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-cache.json")

	before := time.Now()
	if err := SaveIndexCache(path, nil); err != nil {
		t.Fatalf("SaveIndexCache: %v", err)
	}
	after := time.Now()

	loaded, err := LoadIndexCache(path)
	if err != nil {
		t.Fatalf("LoadIndexCache: %v", err)
	}
	if loaded.Timestamp.Before(before) || loaded.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in [%v, %v]", loaded.Timestamp, before, after)
	}
}
