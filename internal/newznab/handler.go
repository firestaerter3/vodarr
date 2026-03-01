package newznab

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
		results = h.idx.SearchByIMDB(imdbID)
		slog.Debug("movie search by imdb", "imdb", imdbID, "hits", len(results))

	case tmdbID != "":
		results = h.idx.SearchByTMDB(tmdbID)
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

	offset, _ := strconv.Atoi(q.Get("offset"))
	h.writeRSS(w, results, offset)
}

func (h *Handler) handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tvdbID := q.Get("tvdbid")
	tmdbID := q.Get("tmdbid")
	query := q.Get("q")
	year := q.Get("year")

	var results []*index.Item

	switch {
	case tvdbID != "":
		results = h.idx.SearchByTVDB(tvdbID)
		slog.Debug("tv search by tvdb", "tvdb", tvdbID, "hits", len(results))

	case tmdbID != "":
		results = h.idx.SearchByTMDB(tmdbID)
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

	offset, _ := strconv.Atoi(q.Get("offset"))
	h.writeRSS(w, results, offset)
}

func (h *Handler) handleTextSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("q")
	year := q.Get("year")
	cat := q.Get("cat")

	var mediaType index.MediaType
	if strings.Contains(cat, "5") {
		mediaType = index.TypeSeries
	} else if strings.Contains(cat, "2") {
		mediaType = index.TypeMovie
	}

	results := h.idx.SearchByTitle(query, year, mediaType, 50)
	slog.Debug("text search", "q", query, "cat", cat, "hits", len(results))

	offset, _ := strconv.Atoi(q.Get("offset"))
	h.writeRSS(w, results, offset)
}

// handleGet returns a small JSON descriptor that the qBit handler uses
// when Sonarr/Radarr send a "grab" request.
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	idStr := q.Get("id")
	mediaType := q.Get("type")

	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	// Find item
	var found *index.Item
	for _, item := range h.idx.All() {
		if item.XtreamID == id && (mediaType == "" || string(item.Type) == mediaType) {
			found = item
			break
		}
	}

	if found == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Return JSON descriptor — qBit handler will parse this
	desc := map[string]interface{}{
		"xtream_id":     found.XtreamID,
		"type":          string(found.Type),
		"name":          found.Name,
		"year":          found.Year,
		"imdb_id":       found.IMDBId,
		"tvdb_id":       found.TVDBId,
		"tmdb_id":       found.TMDBId,
		"container_ext": found.ContainerExt,
		"episodes":      found.Episodes,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(desc)
}

func (h *Handler) writeRSS(w http.ResponseWriter, items []*index.Item, offset int) {
	rss := buildRSS(h.serverURL, items, offset, len(items))
	h.writeXML(w, rss)
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
