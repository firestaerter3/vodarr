package sync

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-snapshot.json")

	snap := &Snapshot{
		Timestamp: time.Now().UTC().Truncate(time.Second),
		Movies: map[int]MovieEntry{
			42: {Name: "The Matrix", Checksum: MovieChecksum("The Matrix", "mkv")},
		},
		Series: map[int]SeriesEntry{
			7: {
				Name:         "Breaking Bad",
				LastModified: "1700000000",
				EpisodeCount: 62,
				Checksum:     SeriesChecksum("Breaking Bad", "1700000000", 62),
				Episodes: []index.EpisodeItem{
					{EpisodeID: 101, Season: 1, EpisodeNum: 1, Title: "Pilot", Ext: "mkv"},
					{EpisodeID: 102, Season: 1, EpisodeNum: 2, Title: "Cat's in the Bag", Ext: "mkv"},
				},
			},
		},
	}

	if err := SaveSnapshot(path, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	loaded, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadSnapshot returned nil")
	}

	// Movies
	if len(loaded.Movies) != 1 {
		t.Errorf("Movies len = %d, want 1", len(loaded.Movies))
	}
	m, ok := loaded.Movies[42]
	if !ok {
		t.Fatal("movie id 42 missing")
	}
	if m.Name != "The Matrix" {
		t.Errorf("movie name = %q, want %q", m.Name, "The Matrix")
	}
	if m.Checksum != MovieChecksum("The Matrix", "mkv") {
		t.Errorf("movie checksum mismatch")
	}

	// Series
	if len(loaded.Series) != 1 {
		t.Errorf("Series len = %d, want 1", len(loaded.Series))
	}
	se, ok := loaded.Series[7]
	if !ok {
		t.Fatal("series id 7 missing")
	}
	if se.Name != "Breaking Bad" {
		t.Errorf("series name = %q, want %q", se.Name, "Breaking Bad")
	}
	if se.EpisodeCount != 62 {
		t.Errorf("episode count = %d, want 62", se.EpisodeCount)
	}
	if len(se.Episodes) != 2 {
		t.Errorf("episodes len = %d, want 2", len(se.Episodes))
	}
	if se.Episodes[0].Title != "Pilot" {
		t.Errorf("first episode title = %q, want %q", se.Episodes[0].Title, "Pilot")
	}
}

func TestLoadSnapshotMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if snap != nil {
		t.Fatalf("expected nil snapshot for missing file, got %+v", snap)
	}
}

func TestChecksumStability(t *testing.T) {
	// Same inputs must always produce the same hash.
	c1 := MovieChecksum("Inception", "mkv")
	c2 := MovieChecksum("Inception", "mkv")
	if c1 != c2 {
		t.Errorf("MovieChecksum not stable: %q != %q", c1, c2)
	}

	s1 := SeriesChecksum("The Wire", "1699000000", 60)
	s2 := SeriesChecksum("The Wire", "1699000000", 60)
	if s1 != s2 {
		t.Errorf("SeriesChecksum not stable: %q != %q", s1, s2)
	}

	// Different inputs must produce different hashes.
	if MovieChecksum("Inception", "mkv") == MovieChecksum("Inception", "mp4") {
		t.Error("MovieChecksum collision on different ext")
	}
	if SeriesChecksum("The Wire", "1699000000", 60) == SeriesChecksum("The Wire", "1699000001", 60) {
		t.Error("SeriesChecksum collision on different lastModified")
	}
}

func TestSaveSnapshotAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vodarr-snapshot.json")

	snap := &Snapshot{
		Timestamp: time.Now(),
		Movies:    map[int]MovieEntry{},
		Series:    map[int]SeriesEntry{},
	}

	if err := SaveSnapshot(path, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file in dir after save, got %d: %v", len(entries), entries)
	}
	if entries[0].Name() != ".vodarr-snapshot.json" {
		t.Errorf("unexpected file: %s", entries[0].Name())
	}
}
