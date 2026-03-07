package xtream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is an Xtream Codes API client.
type Client struct {
	baseURL        string
	username       string
	password       string
	http           *http.Client
	requestDelay   time.Duration // inter-request pacing (applied once per apiGet call)
	retryBaseDelay time.Duration // base delay for exponential backoff
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		username:       username,
		password:       password,
		http:           &http.Client{Timeout: 30 * time.Second},
		requestDelay:   50 * time.Millisecond,
		retryBaseDelay: 1 * time.Second,
	}
}

// UserInfo is returned by the authentication endpoint.
type UserInfo struct {
	UserInfo struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		Status        string `json:"status"`
		ExpDate       string `json:"exp_date"`
		MaxConnections FlexInt `json:"max_connections"`
		ActiveCons    FlexInt `json:"active_cons"`
	} `json:"user_info"`
	ServerInfo struct {
		URL      string `json:"url"`
		Port     string `json:"port"`
		HTTPSPort string `json:"https_port"`
		Protocol string `json:"server_protocol"`
	} `json:"server_info"`
}

// Category represents a VOD or series category.
type Category struct {
	ID   string `json:"category_id"`
	Name string `json:"category_name"`
}

// VODStream is a single VOD entry from the Xtream catalog.
type VODStream struct {
	ID           FlexInt `json:"stream_id"`
	Name         string  `json:"name"`
	CategoryID   string  `json:"category_id"`
	ContainerExt string  `json:"container_extension"`
	CustomSID    string  `json:"custom_sid"`
	Added        string  `json:"added"`
	TMDBId       FlexInt `json:"tmdb"`
	Rating       FlexFloat `json:"rating"`
	Plot         string  `json:"plot"`
	Cast         string  `json:"cast"`
	Director     string  `json:"director"`
	Genre        string  `json:"genre"`
	ReleaseDate  string  `json:"releaseDate"`
	Duration     string  `json:"duration"`
	Backdrop     string  `json:"backdrop_path"`
	Poster       string  `json:"stream_icon"`
	Year         string  `json:"year"`
}

// VODInfo holds detailed info about a single VOD item.
type VODInfo struct {
	Info    VODInfoDetail `json:"info"`
	MovieData struct {
		StreamID     int    `json:"stream_id"`
		Name         string `json:"name"`
		ContainerExt string `json:"container_extension"`
	} `json:"movie_data"`
}

type VODInfoDetail struct {
	TMDBId      FlexInt          `json:"tmdb_id"`
	Name        string           `json:"name"`
	Plot        string           `json:"plot"`
	Cast        string           `json:"cast"`
	Director    string           `json:"director"`
	Genre       string           `json:"genre"`
	ReleaseDate string           `json:"releasedate"`
	Rating      FlexFloat        `json:"rating"`
	Duration    string           `json:"duration"`
	Backdrop    []string         `json:"backdrop_path"`
	Bitrate     int              `json:"bitrate"`
	Video       VODVideoInfo     `json:"video"`
}

// VODVideoInfo holds the video stream metadata returned by get_vod_info.
type VODVideoInfo struct {
	Tags map[string]string `json:"tags"`
}

// Series is a single TV series entry.
type Series struct {
	SeriesID   FlexInt   `json:"series_id"`
	Name       string    `json:"name"`
	CategoryID string    `json:"category_id"`
	Cover      string    `json:"cover"`
	Plot       string    `json:"plot"`
	Cast       string    `json:"cast"`
	Director   string    `json:"director"`
	Genre      string    `json:"genre"`
	ReleaseDate string   `json:"releaseDate"`
	LastModified string  `json:"last_modified"`
	Rating     FlexFloat `json:"rating"`
	TMDBId     FlexInt   `json:"tmdb"`
	YouTubeTrailer string `json:"youtube_trailer"`
	Backdrop   []string  `json:"backdrop_path"`
}

// SeriesInfo holds seasons and episodes for a series.
type SeriesInfo struct {
	Info     SeriesInfoDetail      `json:"info"`
	Seasons  json.RawMessage       `json:"seasons"`  // provider may return [] or {} — field unused
	Episodes map[string][]Episode  `json:"episodes"`
}

type SeriesInfoDetail struct {
	Name        string    `json:"name"`
	Cover       string    `json:"cover"`
	Plot        string    `json:"plot"`
	Cast        string    `json:"cast"`
	Director    string    `json:"director"`
	Genre       string    `json:"genre"`
	ReleaseDate string    `json:"releasedate"`
	Rating      FlexFloat `json:"rating"`
	TMDBId      FlexInt   `json:"tmdb_id"`
	Backdrop    []string  `json:"backdrop_path"`
}

type SeasonInfo struct {
	Name          string `json:"name"`
	EpisodeCount  FlexInt `json:"episode_count"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	Cover         string `json:"cover"`
	SeasonNumber  FlexInt `json:"season_number"`
}

type Episode struct {
	ID            FlexInt        `json:"id"`
	EpisodeNum    FlexInt        `json:"episode_num"`
	Title         string         `json:"title"`
	ContainerExt  string         `json:"container_extension"`
	Info          EpisodeInfo    `json:"info"`
	Added         string         `json:"added"`
	Season        FlexInt        `json:"season"`
}

type EpisodeInfo struct {
	TMDBId       FlexInt           `json:"tmdb_id"`
	ReleaseDate  string            `json:"releasedate"`
	Plot         string            `json:"plot"`
	Duration     string            `json:"duration"`
	DurationSecs int               `json:"duration_secs"`
	Bitrate      int               `json:"bitrate"`
	MovieImage   string            `json:"movie_image"`
	Rating       FlexFloat         `json:"rating"`
	Video        EpisodeVideoInfo  `json:"video"`
}

// EpisodeVideoInfo holds per-episode video stream metadata from get_series_info.
// Tags is a freeform map; the key "NUMBER_OF_BYTES-eng" (or "NUMBER_OF_BYTES")
// contains the exact file size in bytes when the provider includes it.
type EpisodeVideoInfo struct {
	Tags map[string]string `json:"tags"`
}

// Authenticate validates the credentials and returns server info.
func (c *Client) Authenticate(ctx context.Context) (*UserInfo, error) {
	var info UserInfo
	if err := c.apiGet(ctx, "", nil, &info); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	return &info, nil
}

// GetVODCategories returns all VOD categories.
func (c *Client) GetVODCategories(ctx context.Context) ([]Category, error) {
	var cats []Category
	if err := c.apiGet(ctx, "get_vod_categories", nil, &cats); err != nil {
		return nil, fmt.Errorf("get vod categories: %w", err)
	}
	return cats, nil
}

// GetVODStreams returns all VOD streams, optionally filtered by category.
func (c *Client) GetVODStreams(ctx context.Context, categoryID string) ([]VODStream, error) {
	params := url.Values{}
	if categoryID != "" {
		params.Set("category_id", categoryID)
	}
	var streams []VODStream
	if err := c.apiGet(ctx, "get_vod_streams", params, &streams); err != nil {
		return nil, fmt.Errorf("get vod streams: %w", err)
	}
	return streams, nil
}

// GetVODInfo returns detailed information about a specific VOD stream.
func (c *Client) GetVODInfo(ctx context.Context, id int) (*VODInfo, error) {
	params := url.Values{"vod_id": {strconv.Itoa(id)}}
	var info VODInfo
	if err := c.apiGet(ctx, "get_vod_info", params, &info); err != nil {
		return nil, fmt.Errorf("get vod info %d: %w", id, err)
	}
	return &info, nil
}

// GetSeriesCategories returns all series categories.
func (c *Client) GetSeriesCategories(ctx context.Context) ([]Category, error) {
	var cats []Category
	if err := c.apiGet(ctx, "get_series_categories", nil, &cats); err != nil {
		return nil, fmt.Errorf("get series categories: %w", err)
	}
	return cats, nil
}

// GetSeries returns all series, optionally filtered by category.
func (c *Client) GetSeries(ctx context.Context, categoryID string) ([]Series, error) {
	params := url.Values{}
	if categoryID != "" {
		params.Set("category_id", categoryID)
	}
	var series []Series
	if err := c.apiGet(ctx, "get_series", params, &series); err != nil {
		return nil, fmt.Errorf("get series: %w", err)
	}
	return series, nil
}

// GetSeriesInfo returns detailed information about a specific series.
func (c *Client) GetSeriesInfo(ctx context.Context, id int) (*SeriesInfo, error) {
	params := url.Values{"series_id": {strconv.Itoa(id)}}
	var info SeriesInfo
	if err := c.apiGet(ctx, "get_series_info", params, &info); err != nil {
		return nil, fmt.Errorf("get series info %d: %w", id, err)
	}
	return &info, nil
}

// StreamURL builds the stream URL for a VOD item.
// Credentials are path-escaped to handle special characters safely (4H).
func (c *Client) StreamURL(streamID int, ext string) string {
	if ext == "" {
		ext = "mkv"
	}
	return fmt.Sprintf("%s/movie/%s/%s/%d.%s",
		c.baseURL, url.PathEscape(c.username), url.PathEscape(c.password), streamID, ext)
}

// SeriesStreamURL builds the stream URL for a series episode.
// Credentials are path-escaped to handle special characters safely (4H).
func (c *Client) SeriesStreamURL(episodeID int, ext string) string {
	if ext == "" {
		ext = "mkv"
	}
	return fmt.Sprintf("%s/series/%s/%s/%d.%s",
		c.baseURL, url.PathEscape(c.username), url.PathEscape(c.password), episodeID, ext)
}

// xtreamCatalogMaxBytes is the maximum catalog response size we'll read.
const xtreamCatalogMaxBytes = 100 << 20 // 100 MB

func (c *Client) apiGet(ctx context.Context, action string, params url.Values, out interface{}) error {
	u := fmt.Sprintf("%s/player_api.php", c.baseURL)

	q := url.Values{
		"username": {c.username},
		"password": {c.password},
	}
	if action != "" {
		q.Set("action", action)
	}
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}

	// Inter-request pacing: applied once before the first attempt.
	if c.requestDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.requestDelay):
		}
	}

	var lastErr error
	for attempt := 0; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Exponential backoff before retry attempts (not before the first attempt).
		if attempt > 0 {
			delay := c.retryBaseDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
		if err != nil {
			return err // Don't retry on request build errors.
		}

		resp, err := c.http.Do(req)
		if err != nil {
			// 2G: Sanitize error to avoid leaking credentials from URL
			lastErr = fmt.Errorf("http get %s: %w", sanitizeURL(u), err)
			continue // Retry on network errors.
		}

		// Retry on 429 and server errors.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status %d for %s", resp.StatusCode, sanitizeURL(u))
			continue
		}

		// Non-retryable HTTP error.
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, sanitizeURL(u))
		}

		// Success: decode and return.
		defer resp.Body.Close()
		// 2H: Limit catalog response body size
		return json.NewDecoder(io.LimitReader(resp.Body, xtreamCatalogMaxBytes)).Decode(out)
	}

	return fmt.Errorf("after 3 retries: %w", lastErr)
}

// EstimateMovieFileSize calls GetVODInfo and estimates the file size from
// bitrate × duration. Returns 0 if the provider does not supply both fields.
func (c *Client) EstimateMovieFileSize(ctx context.Context, streamID int) int64 {
	info, err := c.GetVODInfo(ctx, streamID)
	if err != nil || info == nil {
		return 0
	}
	bitrate := int64(info.Info.Bitrate) // kbps
	duration := parseDurationTag(info.Info.Video.Tags["DURATION"])
	if bitrate <= 0 || duration <= 0 {
		return 0
	}
	return bitrate * 1000 / 8 * duration
}

// parseDurationTag parses an ffprobe-style duration string such as
// "02:50:00.148000000" or "00:50:09" and returns whole seconds.
func parseDurationTag(s string) int64 {
	s = strings.SplitN(s, ".", 2)[0] // strip sub-second fraction
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	sec, _ := strconv.Atoi(parts[2])
	return int64(h*3600 + m*60 + sec)
}

// sanitizeURL strips username and password query params from a URL string
// to prevent credential leakage in error messages.
func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid url>"
	}
	q := u.Query()
	if q.Get("username") != "" {
		q.Set("username", "<redacted>")
	}
	if q.Get("password") != "" {
		q.Set("password", "<redacted>")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// FlexInt handles Xtream providers that send integers as strings or numbers.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" || s == "false" || s == "0" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// Try float truncation
		fv, err2 := strconv.ParseFloat(s, 64)
		if err2 != nil {
			*f = 0
			return nil
		}
		n = int(fv)
	}
	*f = FlexInt(n)
	return nil
}

func (f FlexInt) Int() int { return int(f) }

// FlexFloat handles providers that send floats as strings or numbers.
type FlexFloat float64

func (f *FlexFloat) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" || s == "false" {
		*f = 0
		return nil
	}
	fv, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = FlexFloat(fv)
	return nil
}
