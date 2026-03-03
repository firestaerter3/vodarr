package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

// errNotFound is returned by get() when the TMDB API responds with 404.
// Callers use this to avoid caching not-found responses (a 404 may be
// transient, so caching it for 24 h would hide valid data for a full day).
var errNotFound = errors.New("not found")

// Client is a TMDB API v3 client with rate limiting and caching.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	limiter *time.Ticker

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	data      interface{}
	expiresAt time.Time
}

const (
	cacheTTL     = 24 * time.Hour
	maxCacheSize = 100_000 // 4G: maximum entries before eviction
)

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
		// 40 req/s max — use 30 to be safe
		limiter: time.NewTicker(time.Second / 30),
		cache:   make(map[string]cacheEntry),
	}
}

// Validate checks that the API key is accepted by TMDB.
// It bypasses the rate limiter and cache, making a direct HTTP request.
func (c *Client) Validate(ctx context.Context) error {
	u := c.baseURL + "/authentication"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http get /authentication: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid TMDB API key")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from TMDB", resp.StatusCode)
	}
	return nil
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

// Stop releases the rate limiter ticker (5B).
func (c *Client) Stop() {
	c.limiter.Stop()
}

// SearchMovie searches for movies by title and optional year.
// Returns the best match (first result).
func (c *Client) SearchMovie(ctx context.Context, title string, year int) (*MovieSearchResult, error) {
	key := fmt.Sprintf("search_movie:%s:%d", title, year)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*MovieSearchResult), nil
	}

	params := url.Values{
		"query":         {title},
		"include_adult": {"false"},
		"language":      {"en-US"},
		"page":          {"1"},
	}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}

	var resp movieSearchResponse
	if err := c.get(ctx, "/search/movie", params, &resp); err != nil {
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
func (c *Client) SearchTV(ctx context.Context, title string, year int) (*TVSearchResult, error) {
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
	if err := c.get(ctx, "/search/tv", params, &resp); err != nil {
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
func (c *Client) GetMovieExternalIDs(ctx context.Context, tmdbID int) (*ExternalIDs, error) {
	key := fmt.Sprintf("movie_ext:%d", tmdbID)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*ExternalIDs), nil
	}

	var ids ExternalIDs
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/external_ids", tmdbID), nil, &ids); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil // 404 is not cached — may be transient
		}
		return nil, fmt.Errorf("movie external ids %d: %w", tmdbID, err)
	}
	ids.TMDBId = tmdbID
	c.cachePut(key, &ids)
	return &ids, nil
}

// GetTVExternalIDs fetches IMDB and TVDB IDs for a TV show.
func (c *Client) GetTVExternalIDs(ctx context.Context, tmdbID int) (*ExternalIDs, error) {
	key := fmt.Sprintf("tv_ext:%d", tmdbID)
	if hit, ok := c.cacheGet(key); ok {
		if hit == nil {
			return nil, nil
		}
		return hit.(*ExternalIDs), nil
	}

	var ids ExternalIDs
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/external_ids", tmdbID), nil, &ids); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil // 404 is not cached — may be transient
		}
		return nil, fmt.Errorf("tv external ids %d: %w", tmdbID, err)
	}
	ids.TMDBId = tmdbID
	c.cachePut(key, &ids)
	return &ids, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out interface{}) error {
	// Rate limit
	select {
	case <-c.limiter.C:
	case <-ctx.Done():
		return ctx.Err()
	}

	u := c.baseURL + path
	q := url.Values{}
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http get %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
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
	// 4G: Bound cache size — clear when over limit
	if len(c.cache) >= maxCacheSize {
		c.cache = make(map[string]cacheEntry)
	}
	c.cache[key] = cacheEntry{data: val, expiresAt: time.Now().Add(cacheTTL)}
}
