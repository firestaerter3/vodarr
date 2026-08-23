package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

// These cover the regression behind the vodarr restart loop: SaveIndexCache
// marshalled the entire IndexCache to a []byte before writing, so the transient
// peak was a multiple of the encoded document. Against the container's 256M
// limit that did not fit, and the process was OOM-killed inside SaveIndexCache
// on every completed sync (395 kills against 395 "sync complete" lines).
//
// The fix encodes the items array element-wise. The property that matters is
// therefore not "it is faster" but "peak no longer tracks catalogue size", which
// is what TestSaveIndexCache_AllocatesFarLessThanWholeDocument asserts against
// the previous implementation kept below.

// bigItems builds a catalogue whose per-item payload is representative: Plot,
// Poster and CanonicalName are what make the real 22,400 items expensive.
func bigItems(n int) []*index.Item {
	plot := strings.Repeat("a plot sentence that is not short. ", 30) // ~1 KB
	out := make([]*index.Item, n)
	for i := range out {
		out[i] = &index.Item{
			Type:          index.TypeMovie,
			XtreamID:      i,
			Name:          "Some Title With A Reasonably Long Name " + strings.Repeat("x", 40),
			TMDBId:        "12345",
			IMDBId:        "tt1234567",
			CanonicalName: "Canonical Title " + strings.Repeat("y", 40),
			Year:          "2026",
			Plot:          plot,
			Genre:         "Drama, Thriller",
			Rating:        7.5,
			Poster:        "https://image.example/" + strings.Repeat("z", 80) + ".jpg",
			ReleaseDate:   "2026-01-01",
		}
	}
	return out
}

// saveIndexCacheWholeDocument is the PREVIOUS implementation, kept verbatim so
// the memory claim is measured against it rather than asserted. Do not "clean
// this up" -- a comparison against nothing proves nothing.
func saveIndexCacheWholeDocument(path string, items []*index.Item, syncGen int, lastSync time.Time, history []SyncRun) error {
	c := &IndexCache{
		Timestamp:      time.Now(),
		Items:          items,
		SyncGeneration: syncGen,
		LastSync:       lastSync,
		SyncHistory:    history,
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

// peakHeapDuring samples HeapAlloc while fn runs and returns the high-water
// mark above the pre-call baseline.
//
// TotalAlloc will not do here, and an earlier version of this test used it. It
// is CUMULATIVE bytes allocated, not peak live heap, and peak live heap is the
// quantity the OOM killer acts on. An implementation that holds one whole
// document while allocating fewer times in total would pass a TotalAlloc ratio
// while still being killed, which is precisely the bug this guards.
func peakHeapDuring(t *testing.T, fn func()) uint64 {
	t.Helper()
	var base runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&base)

	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > base.HeapAlloc {
				if d := ms.HeapAlloc - base.HeapAlloc; d > peak.Load() {
					peak.Store(d)
				}
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	fn()
	close(stop)
	<-done
	return peak.Load()
}

func TestSaveIndexCache_AllocatesFarLessThanWholeDocument(t *testing.T) {
	const n = 5000
	items := bigItems(n)
	hist := []SyncRun{{StartedAt: time.Now(), DurationMs: 1234, Found: n, Enriched: n}}

	dir := t.TempDir()
	newPath := filepath.Join(dir, "new.json")
	oldPath := filepath.Join(dir, "old.json")

	newAlloc := peakHeapDuring(t, func() {
		if err := SaveIndexCache(newPath, items, 7, time.Now(), hist); err != nil {
			t.Fatalf("SaveIndexCache: %v", err)
		}
	})
	oldAlloc := peakHeapDuring(t, func() {
		if err := saveIndexCacheWholeDocument(oldPath, items, 7, time.Now(), hist); err != nil {
			t.Fatalf("old impl: %v", err)
		}
	})

	fi, err := os.Stat(newPath)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(fi.Size())

	t.Logf("encoded document %d bytes; element-wise PEAK heap %d, whole-document PEAK heap %d",
		size, newAlloc, oldAlloc)

	// The baseline must peak at at least the whole document, since that is
	// exactly what it builds. If this stops holding, the comparison below has
	// become meaningless and must be re-derived rather than relaxed.
	if oldAlloc < size {
		t.Fatalf("whole-document impl peaked at %d for a %d byte document; "+
			"the baseline no longer behaves as described", oldAlloc, size)
	}

	// The claim: peak must not track the document. Threshold is half the
	// document rather than a ratio against the baseline, because that is the
	// property being asserted -- an implementation holding the whole document
	// fails regardless of how the baseline behaves.
	if newAlloc >= size/2 {
		t.Errorf("element-wise encoding peaked at %d for a %d byte document; "+
			"peak should track the largest single item, not the catalogue "+
			"(baseline peaked at %d)", newAlloc, size, oldAlloc)
	}
}

// The memory property is worthless if the file no longer round-trips, and
// hand-assembling JSON is exactly where that breaks. Covers the omitempty
// branches too, since those are written by hand now.
func TestSaveIndexCache_RoundTrips(t *testing.T) {
	items := bigItems(3)
	hist := []SyncRun{
		{StartedAt: time.Now().UTC().Truncate(time.Second), DurationMs: 10, Found: 3},
		{StartedAt: time.Now().UTC().Truncate(time.Second), DurationMs: 20, Found: 3, Error: "boom"},
	}
	last := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name    string
		items   []*index.Item
		last    time.Time
		history []SyncRun
	}{
		{"full", items, last, hist},
		{"no last_sync", items, time.Time{}, hist},
		{"no history", items, last, nil},
		{"neither", items, time.Time{}, nil},
		{"no items", nil, last, hist},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.json")
			if err := SaveIndexCache(p, tc.items, 42, tc.last, tc.history); err != nil {
				t.Fatalf("save: %v", err)
			}

			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(raw) {
				t.Fatalf("output is not valid JSON:\n%s", truncate(raw))
			}

			got, err := LoadIndexCache(p)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got == nil {
				t.Fatal("load returned nil")
			}
			if got.SyncGeneration != 42 {
				t.Errorf("sync_generation = %d, want 42", got.SyncGeneration)
			}
			if len(got.Items) != len(tc.items) {
				t.Fatalf("items = %d, want %d", len(got.Items), len(tc.items))
			}
			for i := range tc.items {
				if !reflect.DeepEqual(got.Items[i], tc.items[i]) {
					t.Errorf("item %d round-tripped unequal", i)
					break
				}
			}
			if !got.LastSync.Equal(tc.last) {
				t.Errorf("last_sync = %v, want %v", got.LastSync, tc.last)
			}
			if len(got.SyncHistory) != len(tc.history) {
				t.Errorf("sync_history = %d entries, want %d", len(got.SyncHistory), len(tc.history))
			}

			// Shape preservation is asserted properly by
			// TestSaveIndexCache_ShapeMatchesPreviousEncoder, which diffs
			// against the previous encoder rather than guessing which fields
			// omitempty applies to. An earlier version of this test guessed,
			// and guessed wrong: omitempty does not omit a zero time.Time.
		})
	}
}

// A failed write must not leave a temp file behind. Nothing prunes that
// directory, so a leak here accumulates forever.
func TestSaveIndexCache_NoTempFileLeftOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A directory where the final rename target is itself a directory, so
	// os.Rename fails after the content is written.
	target := filepath.Join(dir, "c.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveIndexCache(target, bigItems(2), 1, time.Now(), nil); err == nil {
		t.Fatal("expected an error when the target is a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vodarr-cache-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func truncate(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "..."
	}
	return string(b)
}

// TestSaveIndexCache_ShapeMatchesPreviousEncoder is the strongest guard here.
// Hand-assembling JSON is exactly where a rewrite silently changes the on-disk
// representation, and two such changes were caught in review before this test
// existed: nil items became [] instead of null, and a zero last_sync was
// omitted even though omitempty does not omit a time.Time.
//
// Rather than asserting key-by-key, this runs both encoders on identical inputs
// and requires the decoded documents to be identical once the wall-clock
// Timestamp is removed. Any future shape drift fails here without anyone having
// to anticipate the specific field.
func TestSaveIndexCache_ShapeMatchesPreviousEncoder(t *testing.T) {
	last := time.Now().UTC().Truncate(time.Second)
	hist := []SyncRun{{StartedAt: last, DurationMs: 5, Found: 2, Error: "x"}}

	cases := []struct {
		name    string
		items   []*index.Item
		last    time.Time
		history []SyncRun
	}{
		{"typical", bigItems(4), last, hist},
		{"nil items", nil, last, hist},
		{"empty items", []*index.Item{}, last, hist},
		{"zero last_sync", bigItems(2), time.Time{}, hist},
		{"nil history", bigItems(2), last, nil},
		{"empty history", bigItems(2), last, []SyncRun{}},
		{"everything zero", nil, time.Time{}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			newPath := filepath.Join(dir, "new.json")
			oldPath := filepath.Join(dir, "old.json")

			if err := SaveIndexCache(newPath, tc.items, 9, tc.last, tc.history); err != nil {
				t.Fatalf("new: %v", err)
			}
			if err := saveIndexCacheWholeDocument(oldPath, tc.items, 9, tc.last, tc.history); err != nil {
				t.Fatalf("old: %v", err)
			}

			decode := func(p string) map[string]any {
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				var m map[string]any
				if err := json.Unmarshal(b, &m); err != nil {
					t.Fatalf("%s is not valid JSON: %v\n%s", p, err, truncate(b))
				}
				// Timestamp is time.Now() in both, so it can never match.
				delete(m, "timestamp")
				return m
			}

			gotNew, gotOld := decode(newPath), decode(oldPath)
			if !reflect.DeepEqual(gotNew, gotOld) {
				// Report the key-level difference; a full dump is unreadable.
				for k, ov := range gotOld {
					nv, present := gotNew[k]
					if !present {
						t.Errorf("key %q present in previous encoder output, absent in new", k)
						continue
					}
					if !reflect.DeepEqual(nv, ov) {
						t.Errorf("key %q differs:\n  old: %#v\n  new: %#v", k, ov, nv)
					}
				}
				for k := range gotNew {
					if _, present := gotOld[k]; !present {
						t.Errorf("key %q present in new output, absent in previous encoder", k)
					}
				}
			}
		})
	}
}
