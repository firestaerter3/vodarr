package qbit

import (
	"sync"
	"time"
)

// TorrentState mirrors qBittorrent torrent states.
type TorrentState string

const (
	StateDownloading TorrentState = "downloading"
	StatePausedUP    TorrentState = "pausedUP"
	StateUploading   TorrentState = "uploading"
)

// Torrent represents a tracked "download" in the fake qBit client.
type Torrent struct {
	Hash         string
	Name         string
	SavePath     string
	Category     string
	State        TorrentState
	Progress     float64
	Size         int64
	AddedOn      int64
	CompletionOn int64

	// VODarr-specific: paths to the created files.
	// StrmPaths holds the .strm files (stream URL content, for Plex/Emby).
	// MkvPaths holds the companion empty .mkv stubs (for Sonarr/Radarr import filter).
	StrmPaths []string
	MkvPaths  []string

	// Descriptor from the Newznab /api?t=get response
	XtreamID  int
	MediaType string // "movie" or "series"
	MediaName string
	MediaYear string
	IMDBId    string
	TVDBId    string
	TMDBId    string
	ContainerExt string
}

// Store is an in-memory store for active and completed torrents.
type Store struct {
	mu      sync.RWMutex
	torrents map[string]*Torrent
}

func NewStore() *Store {
	return &Store{torrents: make(map[string]*Torrent)}
}

// Add inserts a new torrent record.
func (s *Store) Add(t *Torrent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.AddedOn = time.Now().Unix()
	s.torrents[t.Hash] = t
}

// Get returns a torrent by hash (nil if not found).
func (s *Store) Get(hash string) *Torrent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.torrents[hash]
}

// All returns snapshots of all tracked torrents.
// Each element is a copy so callers may read fields without holding the lock.
func (s *Store) All() []*Torrent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Torrent, 0, len(s.torrents))
	for _, t := range s.torrents {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

// Delete removes a torrent from tracking.
func (s *Store) Delete(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.torrents, hash)
}

// SetComplete marks a torrent as done and records its strm and mkv paths.
func (s *Store) SetComplete(hash string, strmPaths, mkvPaths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.torrents[hash]
	if !ok {
		return
	}
	t.State = StatePausedUP
	t.Progress = 1.0
	t.CompletionOn = time.Now().Unix()
	t.StrmPaths = strmPaths
	t.MkvPaths = mkvPaths
}
