package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const defaultBaseURL = "https://api4.thetvdb.com/v4"

// Client is a TVDB v4 API client with lazy authentication.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client

	mu    sync.Mutex
	token string
}

// SeriesResult is a single series match from the TVDB search endpoint.
type SeriesResult struct {
	TVDBID int    // numeric TVDB ID
	Name   string // series name as returned by TVDB
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// SearchSeries searches TVDB for a series by title and returns the best match,
// or nil if nothing was found.  Authentication is performed lazily on the first
// call.
func (c *Client) SearchSeries(ctx context.Context, title string) (*SeriesResult, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("tvdb auth: %w", err)
	}

	u := c.baseURL + "/search"
	q := url.Values{"query": {title}, "type": {"series"}}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tvdb search %q: %w", title, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tvdb search %q: status %d", title, resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			TVDBIDStr string `json:"tvdb_id"`
			Name      string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("tvdb search decode: %w", err)
	}
	if len(payload.Data) == 0 {
		return nil, nil
	}

	first := payload.Data[0]
	id, err := strconv.Atoi(first.TVDBIDStr)
	if err != nil || id <= 0 {
		return nil, nil
	}
	return &SeriesResult{TVDBID: id, Name: first.Name}, nil
}

// ensureToken obtains a bearer token if we don't have one yet and returns it.
// Protected by a mutex so concurrent goroutines don't race on login.
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	if err := c.login(ctx); err != nil {
		return "", err
	}
	return c.token, nil
}

// login calls POST /login and stores the resulting token.
// Callers must hold c.mu.
func (c *Client) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"apikey": c.apiKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tvdb login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid TVDB API key")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tvdb login: status %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("tvdb login decode: %w", err)
	}
	if payload.Data.Token == "" {
		return fmt.Errorf("tvdb login: empty token")
	}
	c.token = payload.Data.Token
	return nil
}
