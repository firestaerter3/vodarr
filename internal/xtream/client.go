package xtream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is an Xtream Codes API client.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
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
	TMDBId      FlexInt   `json:"tmdb_id"`
	Name        string    `json:"name"`
	Plot        string    `json:"plot"`
	Cast        string    `json:"cast"`
	Director    string    `json:"director"`
	Genre       string    `json:"genre"`
	ReleaseDate string    `json:"releasedate"`
	Rating      FlexFloat `json:"rating"`
	Duration    string    `json:"duration"`
	Backdrop    []string  `json:"backdrop_path"`
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
	Info    SeriesInfoDetail       `json:"info"`
	Seasons map[string]SeasonInfo  `json:"seasons"`
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
	TMDBId      FlexInt   `json:"tmdb_id"`
	ReleaseDate string    `json:"releasedate"`
	Plot        string    `json:"plot"`
	Duration    string    `json:"duration"`
	MovieImage  string    `json:"movie_image"`
	Rating      FlexFloat `json:"rating"`
}

// Authenticate validates the credentials and returns server info.
func (c *Client) Authenticate() (*UserInfo, error) {
	var info UserInfo
	if err := c.apiGet("", nil, &info); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	return &info, nil
}

// GetVODCategories returns all VOD categories.
func (c *Client) GetVODCategories() ([]Category, error) {
	var cats []Category
	if err := c.apiGet("get_vod_categories", nil, &cats); err != nil {
		return nil, fmt.Errorf("get vod categories: %w", err)
	}
	return cats, nil
}

// GetVODStreams returns all VOD streams, optionally filtered by category.
func (c *Client) GetVODStreams(categoryID string) ([]VODStream, error) {
	params := url.Values{}
	if categoryID != "" {
		params.Set("category_id", categoryID)
	}
	var streams []VODStream
	if err := c.apiGet("get_vod_streams", params, &streams); err != nil {
		return nil, fmt.Errorf("get vod streams: %w", err)
	}
	return streams, nil
}

// GetVODInfo returns detailed information about a specific VOD stream.
func (c *Client) GetVODInfo(id int) (*VODInfo, error) {
	params := url.Values{"vod_id": {strconv.Itoa(id)}}
	var info VODInfo
	if err := c.apiGet("get_vod_info", params, &info); err != nil {
		return nil, fmt.Errorf("get vod info %d: %w", id, err)
	}
	return &info, nil
}

// GetSeriesCategories returns all series categories.
func (c *Client) GetSeriesCategories() ([]Category, error) {
	var cats []Category
	if err := c.apiGet("get_series_categories", nil, &cats); err != nil {
		return nil, fmt.Errorf("get series categories: %w", err)
	}
	return cats, nil
}

// GetSeries returns all series, optionally filtered by category.
func (c *Client) GetSeries(categoryID string) ([]Series, error) {
	params := url.Values{}
	if categoryID != "" {
		params.Set("category_id", categoryID)
	}
	var series []Series
	if err := c.apiGet("get_series", params, &series); err != nil {
		return nil, fmt.Errorf("get series: %w", err)
	}
	return series, nil
}

// GetSeriesInfo returns detailed information about a specific series.
func (c *Client) GetSeriesInfo(id int) (*SeriesInfo, error) {
	params := url.Values{"series_id": {strconv.Itoa(id)}}
	var info SeriesInfo
	if err := c.apiGet("get_series_info", params, &info); err != nil {
		return nil, fmt.Errorf("get series info %d: %w", id, err)
	}
	return &info, nil
}

// StreamURL builds the stream URL for a VOD item.
func (c *Client) StreamURL(streamID int, ext string) string {
	if ext == "" {
		ext = "mkv"
	}
	return fmt.Sprintf("%s/movie/%s/%s/%d.%s",
		c.baseURL, c.username, c.password, streamID, ext)
}

// SeriesStreamURL builds the stream URL for a series episode.
func (c *Client) SeriesStreamURL(episodeID int, ext string) string {
	if ext == "" {
		ext = "mkv"
	}
	return fmt.Sprintf("%s/series/%s/%s/%d.%s",
		c.baseURL, c.username, c.password, episodeID, ext)
}

func (c *Client) apiGet(action string, params url.Values, out interface{}) error {
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

	req, err := http.NewRequest(http.MethodGet, u+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
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
