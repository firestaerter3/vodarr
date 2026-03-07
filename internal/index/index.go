package index

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// MediaType distinguishes movies from TV series.
type MediaType string

const (
	TypeMovie  MediaType = "movie"
	TypeSeries MediaType = "series"
)

// Item is a single indexed media entry (movie or series episode set).
type Item struct {
	// Identity
	Type     MediaType
	XtreamID int    // stream_id (VOD) or series_id (series)
	Name     string // original name from Xtream

	// External IDs (may be empty if enrichment failed)
	TMDBId string
	IMDBId string
	TVDBId string

	// CanonicalName is the authoritative English title from TMDB/TVDB (e.g. "Loving Ibiza: Series").
	// Empty when enrichment has not resolved a canonical title.
	// Used in release titles so Sonarr/Radarr can match by title rather than TVDB ID alone.
	CanonicalName string

	// Metadata
	Year        string
	Plot        string
	Genre       string
	Rating      float64
	Poster      string
	ReleaseDate string

	// For series: season/episode structure
	// For movies: nil
	Episodes []EpisodeItem

	// Stream info
	ContainerExt string // mkv, mp4, ts, etc.
	FileSize     int64  `json:"file_size,omitempty"` // bytes, from HTTP HEAD at sync time
	Duration     float64 `json:"duration,omitempty"` // seconds, from Xtream API

	// Normalised title used for fuzzy matching (set by index on insert)
	normalizedTitle string
}

// EpisodeItem is an episode within a series.
// JSON tags are explicit to match the field names expected by the qBit handler.
type EpisodeItem struct {
	EpisodeID  int     `json:"EpisodeID"`
	Season     int     `json:"Season"`
	EpisodeNum int     `json:"EpisodeNum"`
	Title      string  `json:"Title"`
	Ext        string  `json:"Ext"`
	FileSize   int64   `json:"file_size,omitempty"` // bytes, from HTTP HEAD at sync time
	Duration   float64 `json:"duration,omitempty"`  // seconds, from Xtream API
}

// Index is a thread-safe in-memory content index.
type Index struct {
	mu sync.RWMutex

	byIMDB   map[string][]*Item
	byTVDB   map[string][]*Item
	byTMDB   map[string][]*Item
	byXtream map[string]*Item // composite key "type:id" to avoid movie/series ID collisions
	allItems []*Item
}

func New() *Index {
	return &Index{
		byIMDB:   make(map[string][]*Item),
		byTVDB:   make(map[string][]*Item),
		byTMDB:   make(map[string][]*Item),
		byXtream: make(map[string]*Item),
	}
}

// xtreamKey builds the composite map key for byXtream lookups.
func xtreamKey(t MediaType, id int) string {
	return fmt.Sprintf("%s:%d", t, id)
}

// Replace atomically swaps the entire index content.
func (idx *Index) Replace(items []*Item) {
	byIMDB := make(map[string][]*Item)
	byTVDB := make(map[string][]*Item)
	byTMDB := make(map[string][]*Item)
	byXtream := make(map[string]*Item)

	for _, item := range items {
		item.normalizedTitle = normalizeTitle(item.Name)

		if item.IMDBId != "" {
			byIMDB[item.IMDBId] = append(byIMDB[item.IMDBId], item)
		}
		if item.TVDBId != "" {
			byTVDB[item.TVDBId] = append(byTVDB[item.TVDBId], item)
		}
		if item.TMDBId != "" {
			byTMDB[item.TMDBId] = append(byTMDB[item.TMDBId], item)
		}
		if item.XtreamID > 0 {
			byXtream[xtreamKey(item.Type, item.XtreamID)] = item
		}
	}

	idx.mu.Lock()
	idx.byIMDB = byIMDB
	idx.byTVDB = byTVDB
	idx.byTMDB = byTMDB
	idx.byXtream = byXtream
	idx.allItems = items
	idx.mu.Unlock()
}

// SearchByXtreamID returns the item for the given Xtream stream/series ID.
// mediaType should be "movie" or "series"; if empty, both types are tried.
func (idx *Index) SearchByXtreamID(id int, mediaType string) *Item {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if mediaType != "" {
		return idx.byXtream[xtreamKey(MediaType(mediaType), id)]
	}
	// No type hint: try movie first, then series.
	if item := idx.byXtream[xtreamKey(TypeMovie, id)]; item != nil {
		return item
	}
	return idx.byXtream[xtreamKey(TypeSeries, id)]
}

// SearchByIMDB returns a copy of items matching the given IMDB ID (4E).
func (idx *Index) SearchByIMDB(id string) []*Item {
	idx.mu.RLock()
	src := idx.byIMDB[id]
	out := make([]*Item, len(src))
	copy(out, src)
	idx.mu.RUnlock()
	return out
}

// SearchByTVDB returns a copy of items matching the given TVDB ID (4E).
func (idx *Index) SearchByTVDB(id string) []*Item {
	idx.mu.RLock()
	src := idx.byTVDB[id]
	out := make([]*Item, len(src))
	copy(out, src)
	idx.mu.RUnlock()
	return out
}

// SearchByTMDB returns a copy of items matching the given TMDB ID (4E).
func (idx *Index) SearchByTMDB(id string) []*Item {
	idx.mu.RLock()
	src := idx.byTMDB[id]
	out := make([]*Item, len(src))
	copy(out, src)
	idx.mu.RUnlock()
	return out
}

// SearchByTitle returns items whose title fuzzy-matches the query.
// Optionally filtered by year string (e.g. "2021"). Returns up to maxResults items.
func (idx *Index) SearchByTitle(query string, year string, mediaType MediaType, maxResults int) []*Item {
	if maxResults <= 0 {
		maxResults = 20
	}
	idx.mu.RLock()
	all := idx.allItems
	idx.mu.RUnlock()

	// Empty query: return first N items (browse / RSS mode)
	if strings.TrimSpace(query) == "" {
		results := make([]*Item, 0, maxResults)
		for _, item := range all {
			if len(results) >= maxResults {
				break
			}
			if mediaType != "" && item.Type != mediaType {
				continue
			}
			results = append(results, item)
		}
		return results
	}

	normQuery := normalizeTitle(query)
	candidates := make([]scored, 0)
	for _, item := range all {
		if mediaType != "" && item.Type != mediaType {
			continue
		}
		if year != "" && item.Year != "" && item.Year != year {
			continue
		}
		score := titleSimilarity(normQuery, item.normalizedTitle)
		if score > 0.4 {
			candidates = append(candidates, scored{item, score})
		}
	}

	// Sort descending by score
	sortByScore(candidates)

	results := make([]*Item, 0, maxResults)
	for i, c := range candidates {
		if i >= maxResults {
			break
		}
		results = append(results, c.item)
	}
	return results
}

// Counts returns the number of movies and series in the index.
func (idx *Index) Counts() (movies, series int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, item := range idx.allItems {
		switch item.Type {
		case TypeMovie:
			movies++
		case TypeSeries:
			series++
		}
	}
	return
}

// All returns all items (read-only snapshot).
func (idx *Index) All() []*Item {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]*Item, len(idx.allItems))
	copy(out, idx.allItems)
	return out
}

type scored struct {
	item  *Item
	score float64
}

// sortByScore sorts candidates descending by score using slices.SortFunc (4F).
func sortByScore(candidates []scored) {
	slices.SortFunc(candidates, func(a, b scored) int {
		// Descending: higher score first
		return cmp.Compare(b.score, a.score)
	})
}
