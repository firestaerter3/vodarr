package qbit

import (
	"testing"
)

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
		s.SetComplete("def456", []string{"/data/strm/tv/A Series/Season 01/A.Series.S01E01.strm"})

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
