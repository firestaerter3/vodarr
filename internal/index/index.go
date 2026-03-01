package index

import (
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

	byIMDB  map[string][]*Item
	byTVDB  map[string][]*Item
	byTMDB  map[string][]*Item
	allItems []*Item
}

func New() *Index {
	return &Index{
		byIMDB: make(map[string][]*Item),
		byTVDB: make(map[string][]*Item),
		byTMDB: make(map[string][]*Item),
	}
}

// Replace atomically swaps the entire index content.
func (idx *Index) Replace(items []*Item) {
	byIMDB := make(map[string][]*Item)
	byTVDB := make(map[string][]*Item)
	byTMDB := make(map[string][]*Item)

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
	}

	idx.mu.Lock()
	idx.byIMDB = byIMDB
	idx.byTVDB = byTVDB
	idx.byTMDB = byTMDB
	idx.allItems = items
	idx.mu.Unlock()
}

// SearchByIMDB returns items matching the given IMDB ID.
func (idx *Index) SearchByIMDB(id string) []*Item {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byIMDB[id]
}

// SearchByTVDB returns items matching the given TVDB ID.
func (idx *Index) SearchByTVDB(id string) []*Item {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byTVDB[id]
}

// SearchByTMDB returns items matching the given TMDB ID.
func (idx *Index) SearchByTMDB(id string) []*Item {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byTMDB[id]
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

// sortByScore is a simple insertion sort for small slices.
func sortByScore(candidates []scored) {
	for i := 1; i < len(candidates); i++ {
		key := candidates[i]
		j := i - 1
		for j >= 0 && candidates[j].score < key.score {
			candidates[j+1] = candidates[j]
			j--
		}
		candidates[j+1] = key
	}
}
