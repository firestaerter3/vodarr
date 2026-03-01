package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const baseURL = "https://api.themoviedb.org/3"

// Client is a TMDB API v3 client with rate limiting and caching.
type Client struct {
	apiKey  string
	http    *http.Client
	limiter *time.Ticker

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	data      interface{}
	expiresAt time.Time
}

const cacheTTL = 24 * time.Hour

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
		// 40 req/s max — use 30 to be safe
		limiter: time.NewTicker(time.Second / 30),
		cache:   make(map[string]cacheEntry),
	}
}

// MovieSearchResult is a single result from movie search.
type MovieSearchResult struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	Overview    string `json:"overview"`
}

// TVSearchResult is a single result from TV search.
type TVSearchResult struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	FirstAirDate string `json:"first_air_date"`
	Overview     string `json:"overview"`
}

// ExternalIDs holds the cross-reference IDs for a movie or TV show.
type ExternalIDs struct {
	IMDBID  string `json:"imdb_id"`
	TVDBID  int    `json:"tvdb_id"`
	TMDBId  int    `json:"-"` // set from the request context
}

type movieSearchResponse struct {
	Results []MovieSearchResult `json:"results"`
}

type tvSearchResponse struct {
	Results []TVSearchResult `json:"results"`
}

// SearchMovie searches for movies by title and optional year.
// Returns the best match (first result).
func (c *Client) SearchMovie(title string, year int) (*MovieSearchResult, error) {
	key := fmt.Sprintf("search_movie:%s:%d", title, year)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*MovieSearchResult), nil
	}

	params := url.Values{
		"query":                {title},
		"include_adult":        {"false"},
		"language":             {"en-US"},
		"page":                 {"1"},
	}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}

	var resp movieSearchResponse
	if err := c.get("/search/movie", params, &resp); err != nil {
		return nil, fmt.Errorf("search movie %q: %w", title, err)
	}

	if len(resp.Results) == 0 {
		c.cachePut(key, nil)
		return nil, nil
	}
	result := &resp.Results[0]
	c.cachePut(key, result)
	return result, nil
}

// SearchTV searches for TV shows by title and optional year.
func (c *Client) SearchTV(title string, year int) (*TVSearchResult, error) {
	key := fmt.Sprintf("search_tv:%s:%d", title, year)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*TVSearchResult), nil
	}

	params := url.Values{
		"query":         {title},
		"include_adult": {"false"},
		"language":      {"en-US"},
		"page":          {"1"},
	}
	if year > 0 {
		params.Set("first_air_date_year", strconv.Itoa(year))
	}

	var resp tvSearchResponse
	if err := c.get("/search/tv", params, &resp); err != nil {
		return nil, fmt.Errorf("search tv %q: %w", title, err)
	}

	if len(resp.Results) == 0 {
		c.cachePut(key, nil)
		return nil, nil
	}
	result := &resp.Results[0]
	c.cachePut(key, result)
	return result, nil
}

// GetMovieExternalIDs fetches IMDB and other external IDs for a movie.
func (c *Client) GetMovieExternalIDs(tmdbID int) (*ExternalIDs, error) {
	key := fmt.Sprintf("movie_ext:%d", tmdbID)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*ExternalIDs), nil
	}

	var ids ExternalIDs
	if err := c.get(fmt.Sprintf("/movie/%d/external_ids", tmdbID), nil, &ids); err != nil {
		return nil, fmt.Errorf("movie external ids %d: %w", tmdbID, err)
	}
	ids.TMDBId = tmdbID
	c.cachePut(key, &ids)
	return &ids, nil
}

// GetTVExternalIDs fetches IMDB and TVDB IDs for a TV show.
func (c *Client) GetTVExternalIDs(tmdbID int) (*ExternalIDs, error) {
	key := fmt.Sprintf("tv_ext:%d", tmdbID)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*ExternalIDs), nil
	}

	var ids ExternalIDs
	if err := c.get(fmt.Sprintf("/tv/%d/external_ids", tmdbID), nil, &ids); err != nil {
		return nil, fmt.Errorf("tv external ids %d: %w", tmdbID, err)
	}
	ids.TMDBId = tmdbID
	c.cachePut(key, &ids)
	return &ids, nil
}

func (c *Client) get(path string, params url.Values, out interface{}) error {
	// Rate limit
	<-c.limiter.C

	u := baseURL + path
	q := url.Values{"api_key": {c.apiKey}}
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}

	resp, err := c.http.Get(u + "?" + q.Encode())
	if err != nil {
		return fmt.Errorf("http get %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) cacheGet(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.cache, key)
		return nil, false
	}
	return entry.data, true
}

func (c *Client) cachePut(key string, val interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{data: val, expiresAt: time.Now().Add(cacheTTL)}
}
