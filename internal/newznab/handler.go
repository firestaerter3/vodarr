package newznab

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/vodarr/vodarr/internal/bencode"
	"github.com/vodarr/vodarr/internal/index"
)

// Handler serves the Newznab API.
type Handler struct {
	idx       *index.Index
	apiKey    string
	serverURL string // e.g. "http://vodarr:7878"
}

func NewHandler(idx *index.Index, apiKey, serverURL string) *Handler {
	return &Handler{idx: idx, apiKey: apiKey, serverURL: serverURL}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	if t == "" {
		http.Error(w, "missing t parameter", http.StatusBadRequest)
		return
	}

	// Validate API key if configured
	if h.apiKey != "" {
		key := r.URL.Query().Get("apikey")
		if key == "" {
			key = r.Header.Get("X-Api-Key")
		}
		if key != h.apiKey {
			h.writeError(w, http.StatusUnauthorized, 100, "Incorrect user credentials")
			return
		}
	}

	switch t {
	case "caps":
		h.handleCaps(w, r)
	case "movie":
		h.handleMovieSearch(w, r)
	case "tvsearch":
		h.handleTVSearch(w, r)
	case "search":
		h.handleTextSearch(w, r)
	case "get":
		h.handleGet(w, r)
	default:
		h.writeError(w, http.StatusBadRequest, 202, fmt.Sprintf("No such function (%s)", t))
	}
}

func (h *Handler) handleCaps(w http.ResponseWriter, r *http.Request) {
	caps := buildCaps(h.serverURL)
	h.writeXML(w, caps)
}

func (h *Handler) handleMovieSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	imdbID := q.Get("imdbid")
	tmdbID := q.Get("tmdbid")
	query := q.Get("q")
	year := q.Get("year")

	var results []*index.Item

	switch {
	case imdbID != "":
		// Normalise: Radarr may send "tt1234567" or "1234567"
		if !strings.HasPrefix(imdbID, "tt") {
			imdbID = "tt" + imdbID
		}
		results = filterByType(h.idx.SearchByIMDB(imdbID), index.TypeMovie)
		slog.Debug("movie search by imdb", "imdb", imdbID, "hits", len(results))

	case tmdbID != "":
		results = filterByType(h.idx.SearchByTMDB(tmdbID), index.TypeMovie)
		slog.Debug("movie search by tmdb", "tmdb", tmdbID, "hits", len(results))

	case query != "":
		results = h.idx.SearchByTitle(query, year, index.TypeMovie, 50)
		slog.Debug("movie text search", "q", query, "hits", len(results))

	default:
		// Return all movies (limited)
		all := h.idx.All()
		for _, item := range all {
			if item.Type == index.TypeMovie {
				results = append(results, item)
				if len(results) >= 100 {
					break
				}
			}
		}
	}

	rssItems := buildMovieRSSItems(h.serverURL, results)
	offset, limit := parsePaging(q)
	total := len(rssItems)
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	h.writeRSS(w, rssItems[offset:end], offset, total)
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tvdbID := q.Get("tvdbid")
	tmdbID := q.Get("tmdbid")
	query := q.Get("q")
	year := q.Get("year")
	seasonFilter, _ := strconv.Atoi(q.Get("season"))
	epFilter, _ := strconv.Atoi(q.Get("ep"))

	var results []*index.Item

	switch {
	case tvdbID != "":
		results = filterByType(h.idx.SearchByTVDB(tvdbID), index.TypeSeries)
		slog.Debug("tv search by tvdb", "tvdb", tvdbID, "hits", len(results))

	case tmdbID != "":
		results = filterByType(h.idx.SearchByTMDB(tmdbID), index.TypeSeries)
		slog.Debug("tv search by tmdb", "tmdb", tmdbID, "hits", len(results))

	case query != "":
		results = h.idx.SearchByTitle(query, year, index.TypeSeries, 50)
		slog.Debug("tv text search", "q", query, "hits", len(results))

	default:
		all := h.idx.All()
		for _, item := range all {
			if item.Type == index.TypeSeries {
				results = append(results, item)
				if len(results) >= 100 {
					break
				}
			}
		}
	}

	// 1C: Expand series items into per-episode RSS items with season/episode attrs
	rssItems := buildEpisodeRSSItems(h.serverURL, results, seasonFilter, epFilter)
	offset, limit := parsePaging(q)
	total := len(rssItems)
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	h.writeRSS(w, rssItems[offset:end], offset, total)
}

func (h *Handler) handleTextSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("q")
	year := q.Get("year")
	cat := q.Get("cat")

	var mediaType index.MediaType
	isTV := cat != "" && categoryRangeMatch(cat, 5000, 5999)
	isMovie := cat != "" && categoryRangeMatch(cat, 2000, 2999)
	if isTV && !isMovie {
		mediaType = index.TypeSeries
	} else if isMovie && !isTV {
		mediaType = index.TypeMovie
	}

	results := h.idx.SearchByTitle(query, year, mediaType, 50)
	slog.Debug("text search", "q", query, "cat", cat, "hits", len(results))

	// Build RSS items: movies one-per-item, series per-episode
	var rssItems []Item
	if mediaType == index.TypeSeries {
		rssItems = buildEpisodeRSSItems(h.serverURL, results, 0, 0)
	} else if mediaType == index.TypeMovie {
		rssItems = buildMovieRSSItems(h.serverURL, results)
	} else {
		// Mixed: separate movies from series
		var movies, seriesItems []*index.Item
		for _, item := range results {
			if item.Type == index.TypeMovie {
				movies = append(movies, item)
			} else {
				seriesItems = append(seriesItems, item)
			}
		}
		rssItems = append(buildMovieRSSItems(h.serverURL, movies),
			buildEpisodeRSSItems(h.serverURL, seriesItems, 0, 0)...)
	}

	offset, limit := parsePaging(q)
	total := len(rssItems)
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	h.writeRSS(w, rssItems[offset:end], offset, total)
}

// handleGet returns a small JSON descriptor that the qBit handler uses
// when Sonarr/Radarr send a "grab" request.
// 1C: Accepts episode_id param to return a single-episode descriptor.
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	idStr := q.Get("id")
	mediaType := q.Get("type")
	episodeIDStr := q.Get("episode_id")

	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	episodeID, _ := strconv.Atoi(episodeIDStr)

	found := h.idx.SearchByXtreamID(id, mediaType)
	if found == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// If episode_id is given, filter episodes to just that one
	episodes := found.Episodes
	if episodeID > 0 {
		episodes = nil
		for _, ep := range found.Episodes {
			if ep.EpisodeID == episodeID {
				episodes = []index.EpisodeItem{ep}
				break
			}
		}
		if len(episodes) == 0 {
			http.Error(w, "episode not found", http.StatusNotFound)
			return
		}
	}

	// Don't return a series descriptor with no episodes — the qBit handler
	// would create zero STRM files and Sonarr would see a completed torrent
	// with nothing to import.
	if found.Type == index.TypeSeries && len(episodes) == 0 {
		http.Error(w, "series has no episodes", http.StatusNotFound)
		return
	}

	// Build JSON descriptor with a clean name (IPTV prefix stripped).
	cleanName := stripIPTVPrefix(found.Name)
	desc := map[string]interface{}{
		"xtream_id":     found.XtreamID,
		"type":          string(found.Type),
		"name":          cleanName,
		"year":          found.Year,
		"imdb_id":       found.IMDBId,
		"tvdb_id":       found.TVDBId,
		"tmdb_id":       found.TMDBId,
		"container_ext": found.ContainerExt,
		"episodes":      episodes,
	}

	descJSON, err := json.Marshal(desc)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// info.name encodes the media type and xtream ID for uniqueness.
	infoName := fmt.Sprintf("vodarr-%s-%d", found.Type, found.XtreamID)
	pieces := string(make([]byte, 20)) // 20 zero bytes — valid minimal piece hash
	infoDict := map[string]interface{}{
		"length":       1,
		"name":         infoName,
		"piece length": 262144,
		"pieces":       pieces,
	}
	torrent := map[string]interface{}{
		"comment": string(descJSON),
		"info":    infoDict,
	}

	torrentBytes, err := bencode.Encode(torrent)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Compute and log the info hash so operators can cross-reference with qBit.
	infoEncoded, _ := bencode.Encode(infoDict)
	sum := sha1.Sum(infoEncoded)
	slog.Debug("t=get torrent", "name", cleanName, "info_hash", hex.EncodeToString(sum[:]))

	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.torrent"`, infoName))
	w.Write(torrentBytes)
}

// categoryRangeMatch returns true if any comma-separated category value in cat
// falls within [low, high] inclusive. This avoids the false positive from
// strings.Contains (e.g. "2045" containing "5" would incorrectly match TV).
func categoryRangeMatch(cat string, low, high int) bool {
	for _, part := range strings.Split(cat, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if n >= low && n <= high {
			return true
		}
	}
	return false
}

// filterByType returns only items of the given media type.
func filterByType(items []*index.Item, t index.MediaType) []*index.Item {
	var out []*index.Item
	for _, item := range items {
		if item.Type == t {
			out = append(out, item)
		}
	}
	return out
}

func (h *Handler) writeRSS(w http.ResponseWriter, items []Item, offset, total int) {
	rss := buildRSS(h.serverURL, items, offset, total)
	h.writeXML(w, rss)
}

// parsePaging extracts offset and limit from query params with sane defaults.
func parsePaging(q interface{ Get(string) string }) (offset, limit int) {
	offset, _ = strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	limit, _ = strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	return
}

func (h *Handler) writeXML(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		slog.Error("xml encode error", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code int, msg string) {
	type errResp struct {
		XMLName     xml.Name `xml:"error"`
		Code        int      `xml:"code,attr"`
		Description string   `xml:"description,attr"`
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(errResp{Code: code, Description: msg})
}
