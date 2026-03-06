package qbit

import (
	"testing"
)

func TestTorrentStateLifecycle(t *testing.T) {
	s := NewStore()
	// processURL creates torrents with StateDownloading; verify the full lifecycle.
	s.Add(&Torrent{Hash: "h1", Name: "Test Movie", Size: 1024, State: StateDownloading})

	got := s.Get("h1")
	if got.State != StateDownloading {
		t.Errorf("initial state = %q, want downloading", got.State)
	}
	if got.Progress != 0 {
		t.Errorf("initial progress = %f, want 0", got.Progress)
	}

	s.SetComplete("h1", []string{"/data/strm/movies/Test Movie/Test.Movie.strm"}, []string{"/data/strm/movies/Test Movie/Test.Movie.mkv"})
	got = s.Get("h1")
	if got.State != StatePausedUP {
		t.Errorf("completed state = %q, want pausedUP", got.State)
	}
	if got.Progress != 1.0 {
		t.Errorf("completed progress = %f, want 1.0", got.Progress)
	}
}

func TestContentPathForTorrent(t *testing.T) {
	savePath := "/data/strm"

	t.Run("no strm paths returns save_path", func(t *testing.T) {
		tor := &Torrent{SavePath: savePath}
		got := contentPathForTorrent(tor)
		if got != savePath {
			t.Errorf("got %q, want %q", got, savePath)
		}
	})

	t.Run("single file returns file path", func(t *testing.T) {
		tor := &Torrent{
			SavePath:  savePath,
			StrmPaths: []string{"/data/strm/movies/Inception/Inception.2010.strm"},
		}
		got := contentPathForTorrent(tor)
		if got != "/data/strm/movies/Inception/Inception.2010.strm" {
			t.Errorf("got %q, want file path", got)
		}
	})

	t.Run("multi-file series returns common parent", func(t *testing.T) {
		tor := &Torrent{
			SavePath: savePath,
			StrmPaths: []string{
				"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.strm",
				"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E02.strm",
				"/data/strm/tv/Breaking Bad/Season 02/Breaking.Bad.S02E01.strm",
			},
		}
		got := contentPathForTorrent(tor)
		if got != "/data/strm/tv/Breaking Bad" {
			t.Errorf("got %q, want /data/strm/tv/Breaking Bad", got)
		}
	})

	t.Run("multi-file single season series", func(t *testing.T) {
		tor := &Torrent{
			SavePath: savePath,
			StrmPaths: []string{
				"/data/strm/tv/Fargo/Season 01/Fargo.S01E01.strm",
				"/data/strm/tv/Fargo/Season 01/Fargo.S01E02.strm",
			},
		}
		got := contentPathForTorrent(tor)
		// Common parent of two files in the same dir is that dir
		want := "/data/strm/tv/Fargo/Season 01"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("mkv paths take precedence over strm paths", func(t *testing.T) {
		tor := &Torrent{
			SavePath: savePath,
			StrmPaths: []string{
				"/data/strm/tv/Test/Season 01/Test.S01E01.strm",
			},
			MkvPaths: []string{
				"/data/strm/tv/Test/Season 01/Test.S01E01.mkv",
			},
		}
		got := contentPathForTorrent(tor)
		// MkvPaths has one entry — should return that file path directly
		want := "/data/strm/tv/Test/Season 01/Test.S01E01.mkv"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestStore(t *testing.T) {
	s := NewStore()

	t.Run("add and get", func(t *testing.T) {
		s.Add(&Torrent{Hash: "abc123", Name: "Test Movie", State: StateUploading})
		got := s.Get("abc123")
		if got == nil {
			t.Fatal("expected torrent, got nil")
		}
		if got.Name != "Test Movie" {
			t.Errorf("name = %q, want 'Test Movie'", got.Name)
		}
		if got.AddedOn == 0 {
			t.Error("AddedOn should be set")
		}
	})

	t.Run("get missing", func(t *testing.T) {
		got := s.Get("nonexistent")
		if got != nil {
			t.Error("expected nil for missing hash")
		}
	})

	t.Run("set complete", func(t *testing.T) {
		s.Add(&Torrent{Hash: "def456", Name: "A Series", State: StateUploading, Progress: 0})
		s.SetComplete("def456",
			[]string{"/data/strm/tv/A Series/Season 01/A.Series.S01E01.strm"},
			[]string{"/data/strm/tv/A Series/Season 01/A.Series.S01E01.mkv"})

		got := s.Get("def456")
		if got.State != StatePausedUP {
			t.Errorf("state = %q, want pausedUP", got.State)
		}
		if got.Progress != 1.0 {
			t.Errorf("progress = %f, want 1.0", got.Progress)
		}
		if len(got.StrmPaths) != 1 {
			t.Errorf("strm paths = %d, want 1", len(got.StrmPaths))
		}
		if len(got.MkvPaths) != 1 {
			t.Errorf("mkv paths = %d, want 1", len(got.MkvPaths))
		}
	})

	t.Run("all", func(t *testing.T) {
		s2 := NewStore()
		s2.Add(&Torrent{Hash: "h1", Name: "A"})
		s2.Add(&Torrent{Hash: "h2", Name: "B"})
		all := s2.All()
		if len(all) != 2 {
			t.Errorf("All() = %d items, want 2", len(all))
		}
	})

	t.Run("delete", func(t *testing.T) {
		s2 := NewStore()
		s2.Add(&Torrent{Hash: "todelete", Name: "X"})
		s2.Delete("todelete")
		if s2.Get("todelete") != nil {
			t.Error("torrent should be gone after delete")
		}
	})
}
