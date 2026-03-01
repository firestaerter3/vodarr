package index

import (
	"cmp"
	"slices"
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

	// Normalised title used for fuzzy matching (set by index on insert)
	normalizedTitle string
}

// EpisodeItem is an episode within a series.
type EpisodeItem struct {
	EpisodeID  int
	Season     int
	EpisodeNum int
	Title      string
	Ext        string
}

// Index is a thread-safe in-memory content index.
type Index struct {
	mu sync.RWMutex

	byIMDB    map[string][]*Item
	byTVDB    map[string][]*Item
	byTMDB    map[string][]*Item
	byXtream  map[int]*Item // 5E: O(1) lookup by Xtream stream/series ID
	allItems  []*Item
}

func New() *Index {
	return &Index{
		byIMDB:   make(map[string][]*Item),
		byTVDB:   make(map[string][]*Item),
		byTMDB:   make(map[string][]*Item),
		byXtream: make(map[int]*Item),
	}
}

// Replace atomically swaps the entire index content.
func (idx *Index) Replace(items []*Item) {
	byIMDB := make(map[string][]*Item)
	byTVDB := make(map[string][]*Item)
	byTMDB := make(map[string][]*Item)
	byXtream := make(map[int]*Item)

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
			byXtream[item.XtreamID] = item
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

// SearchByXtreamID returns the item for the given Xtream stream/series ID (5E).
func (idx *Index) SearchByXtreamID(id int) *Item {
	idx.mu.RLock()
	item := idx.byXtream[id]
	idx.mu.RUnlock()
	return item
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
	normQuery := normalizeTitle(query)

	idx.mu.RLock()
	all := idx.allItems
	idx.mu.RUnlock()

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
