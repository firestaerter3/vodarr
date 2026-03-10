package sync

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

// IndexCache persists the fully-enriched index so it can be restored on
// restart, eliminating the 10-20 minute cold-start window.
type IndexCache struct {
	Timestamp       time.Time     `json:"timestamp"`
	Items           []*index.Item `json:"items"`
	SyncGeneration  int           `json:"sync_generation"`
}

// CachePath returns the canonical path for the cache file given the output directory.
func CachePath(outputPath string) string {
	return filepath.Join(outputPath, ".vodarr-cache.json")
}

// LoadIndexCache reads the cache from path. Returns nil, nil if the file does
// not exist. Returns an error (and nil cache) if the file exists but is corrupt.
func LoadIndexCache(path string) (*IndexCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c IndexCache
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Warn("index cache corrupt, ignoring", "path", path, "error", err)
		return nil, err
	}
	return &c, nil
}

// SaveIndexCache writes items and the current sync generation to path atomically (temp file + rename).
func SaveIndexCache(path string, items []*index.Item, syncGen int) error {
	c := &IndexCache{
		Timestamp:      time.Now(),
		Items:          items,
		SyncGeneration: syncGen,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vodarr-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := os.Chmod(tmpName, 0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
