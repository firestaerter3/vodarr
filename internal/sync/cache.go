package sync

import (
	"bufio"
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
	Timestamp      time.Time     `json:"timestamp"`
	Items          []*index.Item `json:"items"`
	SyncGeneration int           `json:"sync_generation"`
	LastSync       time.Time     `json:"last_sync,omitempty"`
	SyncHistory    []SyncRun     `json:"sync_history,omitempty"`
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

// SaveIndexCache writes items, the current sync generation, and sync state
// to path atomically (temp file + rename).
//
// The items array is encoded ONE ELEMENT AT A TIME, straight into the temp
// file, rather than through json.Marshal of the whole IndexCache.
//
// That is not a micro-optimisation, it is the fix for a crash loop. Marshalling
// the whole struct built the entire encoded document in memory first: ~22,400
// enriched items, each carrying Plot, Poster, CanonicalName and, for series, a
// full Episodes slice. json.Marshal grows an internal buffer by doubling and
// then returns a fresh copy of it, so the transient peak is roughly two to three
// times the final JSON. Against the container's 256M limit that did not fit, and
// the kernel killed the process here, inside this function, every single time a
// sync completed: 395 OOM kills against 395 "sync complete" lines, 1:1.
//
// Nothing logged, because a SIGKILL leaves no opportunity to. The symptom looked
// like a clean exit 0 (docker zeroes State.ExitCode on restart, so `docker
// inspect` of the running container could never show otherwise); the evidence
// was only ever in the scope unit's journal.
//
// json.NewEncoder(w).Encode(v) does NOT stream: it marshals to a complete []byte
// and then does a single Write. Swapping Marshal for Encode saves only Marshal's
// trailing full copy, roughly a third, against an overshoot that was never
// measured. Encoding element-wise is what actually bounds the peak, because it
// makes peak track the largest single item instead of the catalogue size, and it
// keeps doing so as the library grows.
//
// The output is ordinary JSON and LoadIndexCache reads it back unchanged.
// Encode's trailing newline is legal whitespace between array elements and
// before a comma.
func SaveIndexCache(path string, items []*index.Item, syncGen int, lastSync time.Time, history []SyncRun) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vodarr-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Every failure past this point must remove the temp file. Previously a
	// partial write left it behind; nothing prunes this directory, so those
	// would have accumulated forever.
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}

	if err := os.Chmod(tmpName, 0600); err != nil {
		return fail(err)
	}

	w := bufio.NewWriterSize(tmp, 1<<20)
	enc := json.NewEncoder(w)

	// Field order matches the IndexCache struct so the file stays readable and
	// diffable, though unmarshalling does not depend on it.
	if _, err := w.WriteString(`{"timestamp":`); err != nil {
		return fail(err)
	}
	if err := enc.Encode(time.Now()); err != nil {
		return fail(err)
	}

	// A nil slice must serialise as null, not [], because that is what
	// json.Marshal did and LoadIndexCache round-trips it back to nil. Writing
	// [] would quietly turn nil into an empty slice on every reload.
	if items == nil {
		if _, err := w.WriteString(`,"items":null`); err != nil {
			return fail(err)
		}
	} else {
		if _, err := w.WriteString(`,"items":[`); err != nil {
			return fail(err)
		}
		for i, it := range items {
			if i > 0 {
				if _, err := w.WriteString(","); err != nil {
					return fail(err)
				}
			}
			if err := enc.Encode(it); err != nil {
				return fail(err)
			}
		}
		if _, err := w.WriteString(`]`); err != nil {
			return fail(err)
		}
	}
	if _, err := w.WriteString(`,"sync_generation":`); err != nil {
		return fail(err)
	}
	if err := enc.Encode(syncGen); err != nil {
		return fail(err)
	}

	// last_sync is tagged omitempty, but omitempty has NO effect on a
	// time.Time: it only omits false, 0, "", a nil pointer/interface, or an
	// empty slice/map, and a struct is none of those. json.Marshal therefore
	// always wrote it, zero value included, as "0001-01-01T00:00:00Z". An
	// earlier draft of this function skipped it when zero, which silently
	// changed the on-disk shape. Always write it.
	if _, err := w.WriteString(`,"last_sync":`); err != nil {
		return fail(err)
	}
	if err := enc.Encode(lastSync); err != nil {
		return fail(err)
	}
	// sync_history is a slice, so here omitempty genuinely does omit. Bounded
	// at 365 entries, so it can stay whole.
	if len(history) > 0 {
		if _, err := w.WriteString(`,"sync_history":`); err != nil {
			return fail(err)
		}
		if err := enc.Encode(history); err != nil {
			return fail(err)
		}
	}

	if _, err := w.WriteString("}"); err != nil {
		return fail(err)
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// A failed rename must clean up too. The previous implementation returned
	// os.Rename's error directly and left the temp file behind; nothing prunes
	// this directory (cleanupExpired only runs when the grace period expires
	// items, and targets the movies/tv subdirectories rather than this one), so
	// those would accumulate indefinitely. Found by a test, not in the wild.
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
