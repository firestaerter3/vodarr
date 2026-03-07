package qbit

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vodarr/vodarr/internal/bencode"
	"github.com/vodarr/vodarr/internal/probe"
	"github.com/vodarr/vodarr/internal/strm"
	"github.com/vodarr/vodarr/internal/xtream"
)

// Prober extracts media metadata from a stream URL.
// It is satisfied by probe.DefaultProber and can be replaced in tests.
type Prober interface {
	Probe(ctx context.Context, url string) (*probe.MediaInfo, error)
}

// itemDescriptor is the JSON structure embedded in both the Newznab JSON
// response (legacy URL path) and the torrent comment field (Torznab path).
type itemDescriptor struct {
	XtreamID     int    `json:"xtream_id"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	Year         string `json:"year"`
	IMDBId       string `json:"imdb_id"`
	TVDBId       string `json:"tvdb_id"`
	TMDBId       string `json:"tmdb_id"`
	ContainerExt string `json:"container_ext"`
	Episodes     []struct {
		EpisodeID  int    `json:"EpisodeID"`
		Season     int    `json:"Season"`
		EpisodeNum int    `json:"EpisodeNum"`
		Title      string `json:"Title"`
		Ext        string `json:"Ext"`
	} `json:"episodes"`
}

const sessionTTL = 24 * time.Hour

// Handler impersonates the qBittorrent Web API v2.
type Handler struct {
	store         *Store
	writer        *strm.Writer
	xtream        *xtream.Client
	prober        Prober              // probes stream URLs for media metadata
	savePath      string
	username      string // 2D: optional credentials; empty = no auth
	password      string
	newznabHost   string // expected host:port of the Newznab server; validated in processURL
	mu            sync.RWMutex
	sessions      map[string]time.Time // sid → last-used time
	categories    map[string]string    // name → savePath; populated by createCategory
	descriptorCli *http.Client        // 2A+3D: dedicated client for descriptor fetches
	mux           *http.ServeMux
}

func NewHandler(store *Store, writer *strm.Writer, xc *xtream.Client, pr Prober, savePath, username, password, newznabURL string) *Handler {
	newznabHost := ""
	if u, err := url.Parse(newznabURL); err == nil {
		newznabHost = u.Host
	}
	h := &Handler{
		store:       store,
		writer:      writer,
		xtream:      xc,
		prober:      pr,
		savePath:    savePath,
		username:    username,
		password:    password,
		newznabHost: newznabHost,
		sessions:    make(map[string]time.Time),
		categories:  make(map[string]string),
		// 2A+3D: dedicated HTTP client for descriptor fetches
		descriptorCli: &http.Client{Timeout: 10 * time.Second},
		mux:           http.NewServeMux(),
	}
	h.registerRoutes()
	return h
}

// authMiddleware checks the SID cookie when credentials are configured.
// The login endpoint is excluded. Sessions older than sessionTTL are evicted.
func (h *Handler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.username == "" {
			next(w, r)
			return
		}
		cookie, err := r.Cookie("SID")
		if err != nil {
			http.Error(w, "Forbidden.", http.StatusForbidden)
			return
		}
		h.mu.Lock()
		lastUsed, ok := h.sessions[cookie.Value]
		if ok && time.Since(lastUsed) > sessionTTL {
			delete(h.sessions, cookie.Value)
			ok = false
		} else if ok {
			h.sessions[cookie.Value] = time.Now()
		}
		h.mu.Unlock()
		if !ok {
			http.Error(w, "Forbidden.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("qbit request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery)
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	auth := h.authMiddleware

	// Auth (login is exempt from auth check)
	h.mux.HandleFunc("POST /api/v2/auth/login", h.handleLogin)

	// App info
	h.mux.HandleFunc("GET /api/v2/app/version", auth(h.handleAppVersion))
	h.mux.HandleFunc("GET /api/v2/app/webapiVersion", auth(h.handleWebapiVersion))
	h.mux.HandleFunc("GET /api/v2/app/preferences", auth(h.handlePreferences))
	h.mux.HandleFunc("GET /api/v2/app/buildInfo", auth(h.handleBuildInfo))

	// Torrents
	h.mux.HandleFunc("POST /api/v2/torrents/add", auth(h.handleTorrentsAdd))
	h.mux.HandleFunc("GET /api/v2/torrents/info", auth(h.handleTorrentsInfo))
	h.mux.HandleFunc("GET /api/v2/torrents/properties", auth(h.handleTorrentsProperties))
	h.mux.HandleFunc("GET /api/v2/torrents/files", auth(h.handleTorrentsFiles))
	h.mux.HandleFunc("POST /api/v2/torrents/delete", auth(h.handleTorrentsDelete))
	h.mux.HandleFunc("POST /api/v2/torrents/pause", auth(h.handleStub))
	h.mux.HandleFunc("POST /api/v2/torrents/resume", auth(h.handleStub))
	h.mux.HandleFunc("GET /api/v2/torrents/categories", auth(h.handleCategories))
	h.mux.HandleFunc("POST /api/v2/torrents/createCategory", auth(h.handleCreateCategory))
	h.mux.HandleFunc("POST /api/v2/torrents/setCategory", auth(h.handleStub))
	h.mux.HandleFunc("POST /api/v2/torrents/setSavePath", auth(h.handleStub))

	// Sync / transfer
	h.mux.HandleFunc("GET /api/v2/sync/maindata", auth(h.handleSyncMaindata))
	h.mux.HandleFunc("GET /api/v2/transfer/info", auth(h.handleTransferInfo))
}

// ---- Auth ----

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.username != "" {
		r.ParseForm()
		u := r.FormValue("username")
		p := r.FormValue("password")
		usernameOK := subtle.ConstantTimeCompare([]byte(u), []byte(h.username)) == 1
		passwordOK := subtle.ConstantTimeCompare([]byte(p), []byte(h.password)) == 1
		if !usernameOK || !passwordOK {
			w.Write([]byte("Fails."))
			return
		}
	}

	sid := randomSID()
	h.mu.Lock()
	h.sessions[sid] = time.Now()
	h.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "SID",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Write([]byte("Ok."))
}

func randomSID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- App info ----

func (h *Handler) handleAppVersion(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("v4.3.2"))
}

func (h *Handler) handleWebapiVersion(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("2.7"))
}

func (h *Handler) handlePreferences(w http.ResponseWriter, r *http.Request) {
	prefs := map[string]interface{}{
		"save_path":                 h.savePath,
		"temp_path_enabled":         false,
		"temp_path":                 h.savePath,
		"incomplete_files_ext":      false,
		"autorun_enabled":           false,
		"autorun_program":           "",
		"queueing_enabled":          false,
		"max_active_downloads":      5,
		"max_active_uploads":        5,
		"max_active_torrents":       5,
		"dont_count_slow_torrents":  false,
	}
	h.writeJSON(w, prefs)
}

func (h *Handler) handleBuildInfo(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]string{
		"qt":       "5.15.2",
		"libtorrent": "1.2.11.0",
		"boost":    "1.75.0",
		"openssl":  "1.1.1i",
		"bitness":  "64",
	})
}

// ---- Torrents add ----

func (h *Handler) handleTorrentsAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		r.ParseForm()
	}

	savePath := r.FormValue("savepath")
	if savePath == "" {
		savePath = h.savePath
	}
	category := r.FormValue("category")

	// Torznab path: Sonarr/Radarr upload the .torrent file directly.
	if r.MultipartForm != nil && len(r.MultipartForm.File["torrents"]) > 0 {
		for _, fh := range r.MultipartForm.File["torrents"] {
			f, err := fh.Open()
			if err != nil {
				slog.Error("torrents/add: open uploaded file", "error", err)
				http.Error(w, "Fails.", http.StatusInternalServerError)
				return
			}
			data, err := io.ReadAll(io.LimitReader(f, descriptorMaxBytes))
			f.Close()
			if err != nil {
				slog.Error("torrents/add: read uploaded file", "error", err)
				http.Error(w, "Fails.", http.StatusInternalServerError)
				return
			}
			if err := h.processTorrentFile(data, savePath, category); err != nil {
				slog.Error("torrents/add: torrent file processing failed", "error", err)
				http.Error(w, "Fails.", http.StatusInternalServerError)
				return
			}
		}
		w.Write([]byte("Ok."))
		return
	}

	// Newznab/URL path (legacy): Sonarr/Radarr send the descriptor URL.
	urls := r.FormValue("urls")
	if urls == "" {
		urls = r.FormValue("url")
	}
	if urls == "" {
		slog.Warn("torrents/add: no urls or torrent files provided")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	for _, rawURL := range strings.Split(strings.TrimSpace(urls), "\n") {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		if err := h.processURL(rawURL, savePath, category); err != nil {
			slog.Error("torrents/add: processing failed", "url", rawURL, "error", err)
			http.Error(w, "Fails.", http.StatusInternalServerError)
			return
		}
	}

	w.Write([]byte("Ok."))
}

// descriptorMaxBytes is the maximum size we'll read from a descriptor response.
const descriptorMaxBytes = 1 << 20 // 1 MB

// processTorrentFile decodes a bencode .torrent file, extracts the JSON
// descriptor from the comment field, and dispatches to processDescriptor.
// The info hash (SHA1 of bencoded info dict) is used as the tracking hash so
// that Sonarr/Radarr can find the torrent by the hash they computed locally.
func (h *Handler) processTorrentFile(data []byte, savePath, category string) error {
	decoded, err := bencode.Decode(data)
	if err != nil {
		return fmt.Errorf("decode torrent: %w", err)
	}
	torrentDict, ok := decoded.(map[string]interface{})
	if !ok {
		return fmt.Errorf("torrent is not a bencode dict")
	}
	commentStr, ok := torrentDict["comment"].(string)
	if !ok {
		return fmt.Errorf("torrent missing comment field")
	}
	infoRaw, ok := torrentDict["info"]
	if !ok {
		return fmt.Errorf("torrent missing info dict")
	}
	infoDict, ok := infoRaw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("torrent info is not a dict")
	}

	// Compute info hash (SHA1 of re-encoded info dict).
	// Re-encoding from the decoded map produces the same bytes as the original
	// because our bencode encoder sorts dict keys deterministically.
	infoEncoded, err := bencode.Encode(infoDict)
	if err != nil {
		return fmt.Errorf("encode info dict: %w", err)
	}
	sum := sha1.Sum(infoEncoded)
	hash := hex.EncodeToString(sum[:])

	var desc itemDescriptor
	if err := json.Unmarshal([]byte(commentStr), &desc); err != nil {
		return fmt.Errorf("parse descriptor: %w", err)
	}
	return h.processDescriptor(desc, hash, savePath, category)
}

// processURL fetches a descriptor from a Newznab t=get URL and dispatches to
// processDescriptor. The response may be either:
//   - a bencode .torrent file (Torznab path): routed to processTorrentFile
//   - a raw JSON descriptor (legacy Newznab path): parsed directly
func (h *Handler) processURL(rawURL, savePath, category string) error {
	// SSRF protection — only allow http/https schemes pointing to ?t=get on the Newznab host
	if err := h.validateDescriptorURL(rawURL); err != nil {
		return fmt.Errorf("invalid descriptor url: %w", err)
	}

	resp, err := h.descriptorCli.Get(rawURL)
	if err != nil {
		return fmt.Errorf("fetch descriptor: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, descriptorMaxBytes))
	if err != nil {
		return fmt.Errorf("read descriptor: %w", err)
	}

	// Bencode dicts start with 'd' — route to processTorrentFile so the
	// info-hash is computed correctly and the descriptor is extracted from
	// the torrent's comment field (same as the multipart Torznab upload path).
	if len(body) > 0 && body[0] == 'd' {
		return h.processTorrentFile(body, savePath, category)
	}

	var desc itemDescriptor
	if err := json.Unmarshal(body, &desc); err != nil {
		return fmt.Errorf("parse descriptor: %w", err)
	}

	hf := fnv.New64a()
	fmt.Fprintf(hf, "%s-%d", desc.Type, desc.XtreamID)
	hash := fmt.Sprintf("%016x", hf.Sum64())

	return h.processDescriptor(desc, hash, savePath, category)
}

// processDescriptor creates a Torrent entry and asynchronously writes .strm files.
func (h *Handler) processDescriptor(desc itemDescriptor, hash string, savePath, category string) error {
	t := &Torrent{
		Hash:         hash,
		Name:         desc.Name,
		SavePath:     savePath,
		Category:     category,
		State:        StateDownloading,
		Progress:     0,
		Size:         1024 * 1024 * 1024,
		XtreamID:     desc.XtreamID,
		MediaType:    desc.Type,
		MediaName:    desc.Name,
		MediaYear:    desc.Year,
		IMDBId:       desc.IMDBId,
		TVDBId:       desc.TVDBId,
		TMDBId:       desc.TMDBId,
		ContainerExt: desc.ContainerExt,
	}
	h.store.Add(t)

	go func() {
		if h.xtream == nil || h.writer == nil {
			slog.Warn("strm write skipped: xtream/writer not configured", "name", desc.Name)
			h.store.SetComplete(hash, nil, nil)
			return
		}

		ctx := context.Background()

		var strmPaths, mkvPaths []string
		var writeErr error

		ext := desc.ContainerExt
		if ext == "" {
			ext = "mkv"
		}

		switch desc.Type {
		case "movie":
			streamURL := h.xtream.StreamURL(desc.XtreamID, ext)
			var info *probe.MediaInfo
			if h.prober != nil {
				var probeErr error
				info, probeErr = h.prober.Probe(ctx, streamURL)
				if probeErr != nil {
					slog.Warn("probe failed, mkv stub will lack metadata", "name", desc.Name, "error", probeErr)
				}
			}
			result, err := h.writer.WriteMovie(desc.Name, desc.Year, streamURL, info)
			if err != nil {
				writeErr = err
			} else {
				strmPaths = append(strmPaths, result.StrmPath)
				mkvPaths = append(mkvPaths, result.MkvPath)
			}

		case "series":
			// Probe only the first episode; all episodes of the same series
			// typically share codec/resolution so we reuse the result.
			var seriesInfo *probe.MediaInfo
			for i, ep := range desc.Episodes {
				epExt := ep.Ext
				if epExt == "" {
					epExt = "mkv"
				}
				streamURL := h.xtream.SeriesStreamURL(ep.EpisodeID, epExt)
				if i == 0 && h.prober != nil {
					var probeErr error
					seriesInfo, probeErr = h.prober.Probe(ctx, streamURL)
					if probeErr != nil {
						slog.Warn("probe failed for series", "name", desc.Name, "error", probeErr)
					}
				}
				// Per-episode info: reuse series codec/resolution from the probe.
				// Duration is kept from the first episode — a reasonable approximation
				// that satisfies Sonarr's sample-detection threshold (requires > ~20 min).
				var epInfo *probe.MediaInfo
				if seriesInfo != nil {
					cp := *seriesInfo
					epInfo = &cp
				}
				result, err := h.writer.WriteEpisode(desc.Name, ep.Season, ep.EpisodeNum, ep.Title, streamURL, epInfo)
				if err != nil {
					slog.Warn("strm write episode failed", "error", err)
					continue
				}
				strmPaths = append(strmPaths, result.StrmPath)
				mkvPaths = append(mkvPaths, result.MkvPath)
			}
		}

		if writeErr != nil {
			slog.Error("strm write failed", "name", desc.Name, "error", writeErr)
		}

		h.store.SetComplete(hash, strmPaths, mkvPaths)
		slog.Info("strm created", "name", desc.Name, "type", desc.Type, "files", len(strmPaths))
	}()

	return nil
}

// validateDescriptorURL rejects non-http/https schemes, URLs that are not
// Newznab t=get descriptor requests, and hosts that don't match the configured
// Newznab server, preventing SSRF.
func (h *Handler) validateDescriptorURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
	if u.Query().Get("t") != "get" {
		return fmt.Errorf("url must be a Newznab t=get request")
	}
	if h.newznabHost != "" && u.Host != h.newznabHost {
		return fmt.Errorf("host %q not allowed: expected %q", u.Host, h.newznabHost)
	}
	return nil
}

// ---- Torrents info ----

func (h *Handler) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	filterHash := r.URL.Query().Get("hashes")

	torrents := h.store.All()
	type qbTorrent struct {
		Hash             string       `json:"hash"`
		Name             string       `json:"name"`
		MagnetURI        string       `json:"magnet_uri"`
		Size             int64        `json:"size"`
		Progress         float64      `json:"progress"`
		DlSpeed          int          `json:"dlspeed"`
		UpSpeed          int          `json:"upspeed"`
		Priority         int          `json:"priority"`
		NumSeeds         int          `json:"num_seeds"`
		NumComplete      int          `json:"num_complete"`
		NumLeechs        int          `json:"num_leechs"`
		NumIncomplete    int          `json:"num_incomplete"`
		Ratio            float64      `json:"ratio"`
		Eta              int          `json:"eta"`
		State            TorrentState `json:"state"`
		Category         string       `json:"category"`
		Tags             string       `json:"tags"`
		SuperSeeding     bool         `json:"super_seeding"`
		ForceStart       bool         `json:"force_start"`
		SavePath         string       `json:"save_path"`
		AddedOn          int64        `json:"added_on"`
		CompletionOn     int64        `json:"completion_on"`
		RatioLimit       float64      `json:"ratio_limit"`
		SeenComplete     int64        `json:"seen_complete"`
		AutoTMM          bool         `json:"auto_tmm"`
		TimeActive       int          `json:"time_active"`
		Downloaded       int64        `json:"downloaded"`
		Uploaded         int64        `json:"uploaded"`
		DownloadedSession int64       `json:"downloaded_session"`
		UploadedSession  int64        `json:"uploaded_session"`
		AmountLeft       int64        `json:"amount_left"`
		ContentPath      string       `json:"content_path"`
		TotalSize        int64        `json:"total_size"`
		Pieces           int          `json:"num_pieces"`
	}

	var out []qbTorrent
	for _, t := range torrents {
		if filterHash != "" && filterHash != "all" && !strings.Contains(filterHash, t.Hash) {
			continue
		}
		downloaded := int64(0)
		if t.Progress >= 1.0 {
			downloaded = t.Size
		}
		out = append(out, qbTorrent{
			Hash:         t.Hash,
			Name:         t.Name,
			Size:         t.Size,
			TotalSize:    t.Size,
			Progress:     t.Progress,
			State:        t.State,
			Category:     t.Category,
			SavePath:     t.SavePath,
			ContentPath:  contentPathForTorrent(t),
			AddedOn:      t.AddedOn,
			CompletionOn: t.CompletionOn,
			Ratio:        1.0,
			Eta:          0,
			Downloaded:   downloaded,
			AmountLeft:   int64((1.0 - t.Progress) * float64(t.Size)),
		})
	}

	if out == nil {
		out = []qbTorrent{}
	}
	h.writeJSON(w, out)
}

func (h *Handler) handleTorrentsProperties(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	t := h.store.Get(hash)
	if t == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	h.writeJSON(w, map[string]interface{}{
		"save_path":         t.SavePath,
		"creation_date":     t.AddedOn,
		"piece_size":        262144,
		"comment":           "",
		"total_wasted":      0,
		"total_uploaded":    t.Size,
		"total_uploaded_session": t.Size,
		"total_downloaded": t.Size,
		"total_downloaded_session": t.Size,
		"up_limit":          -1,
		"dl_limit":          -1,
		"time_elapsed":      0,
		"seeding_time":      0,
		"nb_connections":    0,
		"nb_connections_limit": 100,
		"share_ratio":       1.0,
		"addition_date":     t.AddedOn,
		"completion_date":   t.CompletionOn,
		"created_by":        "VODarr",
		"dl_speed_avg":      0,
		"dl_speed":          0,
		"eta":               0,
		"last_seen":         t.CompletionOn,
		"peers":             0,
		"peers_total":       0,
		"pieces_have":       1,
		"pieces_num":        1,
		"reannounce":        0,
		"seeds":             1,
		"seeds_total":       1,
		"total_size":        t.Size,
		"up_speed_avg":      0,
		"up_speed":          0,
	})
}

func (h *Handler) handleTorrentsFiles(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	t := h.store.Get(hash)
	if t == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	type fileEntry struct {
		Index        int     `json:"index"`
		Name         string  `json:"name"`
		Size         int64   `json:"size"`
		Progress     float64 `json:"progress"`
		Priority     int     `json:"priority"`
		IsSeed       bool    `json:"is_seed"`
		PieceRange   []int   `json:"piece_range"`
		Availability float64 `json:"availability"`
	}

	// Report .mkv paths so Sonarr/Radarr's video-extension filter passes.
	paths := t.MkvPaths
	if len(paths) == 0 {
		paths = t.StrmPaths // fallback if only strm paths are set (legacy)
	}
	var files []fileEntry
	for i, p := range paths {
		name := p
		if rel, err := filepath.Rel(t.SavePath, p); err == nil {
			name = rel
		}
		files = append(files, fileEntry{
			Index:        i,
			Name:         name,
			Size:         64,
			Progress:     1.0,
			Priority:     1,
			IsSeed:       true,
			PieceRange:   []int{0, 0},
			Availability: 1.0,
		})
	}
	if len(files) == 0 {
		// Fallback before files are written
		files = append(files, fileEntry{
			Index:        0,
			Name:         t.Name + ".mkv",
			Size:         64,
			Progress:     1.0,
			Priority:     1,
			IsSeed:       true,
			PieceRange:   []int{0, 0},
			Availability: 1.0,
		})
	}
	h.writeJSON(w, files)
}

func (h *Handler) handleTorrentsDelete(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hashes := r.FormValue("hashes")
	deleteFiles := r.FormValue("deleteFiles") == "true"
	for _, hash := range strings.Split(hashes, "|") {
		hash = strings.TrimSpace(hash)
		if hash != "" {
			t := h.store.Get(hash)
			if t != nil {
				shortHash := hash
				if len(hash) > 8 {
					shortHash = hash[:8]
				}
				slog.Info("torrent deleted by client", "hash", shortHash, "name", t.Name, "state", t.State, "deleteFiles", deleteFiles)
				if deleteFiles {
					// Only delete .mkv stubs — .strm files are permanent streaming
					// content that library symlinks point to; never delete them.
					for _, p := range t.MkvPaths {
						os.Remove(p)
					}
				}
			} else {
				shortHash := hash
				if len(hash) > 8 {
					shortHash = hash[:8]
				}
				slog.Info("torrent delete requested (not in store)", "hash", shortHash)
			}
			h.store.Delete(hash)
		}
	}
	w.Write([]byte("Ok."))
}

func (h *Handler) handleCategories(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	out := make(map[string]interface{}, len(h.categories))
	for name, path := range h.categories {
		out[name] = map[string]string{"name": name, "savePath": path}
	}
	h.mu.RUnlock()
	h.writeJSON(w, out)
}

func (h *Handler) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	name := r.FormValue("category")
	savePath := r.FormValue("savePath")
	if name != "" {
		h.mu.Lock()
		h.categories[name] = savePath
		h.mu.Unlock()
	}
	w.Write([]byte("Ok."))
}

func (h *Handler) handleSyncMaindata(w http.ResponseWriter, r *http.Request) {
	torrents := h.store.All()
	torrentMap := make(map[string]interface{})
	for _, t := range torrents {
		torrentMap[t.Hash] = map[string]interface{}{
			"name":     t.Name,
			"state":    t.State,
			"progress": t.Progress,
			"save_path": t.SavePath,
		}
	}
	h.writeJSON(w, map[string]interface{}{
		"rid":            1,
		"full_update":    true,
		"torrents":       torrentMap,
		"server_state": map[string]interface{}{
			"connection_status": "connected",
			"dht_nodes":         0,
			"dl_info_data":      0,
			"dl_info_speed":     0,
			"dl_rate_limit":     0,
			"up_info_data":      0,
			"up_info_speed":     0,
			"up_rate_limit":     0,
		},
	})
}

func (h *Handler) handleTransferInfo(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]interface{}{
		"connection_status": "connected",
		"dht_nodes":         0,
		"dl_info_data":      0,
		"dl_info_speed":     0,
		"dl_rate_limit":     0,
		"up_info_data":      0,
		"up_info_speed":     0,
		"up_rate_limit":     0,
	})
}

func (h *Handler) handleStub(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Ok."))
}

func (h *Handler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json encode error", "error", err)
	}
}

// contentPathForTorrent returns the appropriate content_path for Sonarr/Radarr.
// Uses MkvPaths (companion stubs) so Sonarr/Radarr's video-extension filter passes.
// For movies (single file) it returns the file path directly.
// For series (multiple files) it returns the common parent directory so that
// Sonarr can scan and import all episodes, not just the first one.
func contentPathForTorrent(t *Torrent) string {
	// Prefer .mkv paths; fall back to .strm paths if mkv not yet populated.
	paths := t.MkvPaths
	if len(paths) == 0 {
		paths = t.StrmPaths
	}
	if len(paths) == 0 {
		return t.SavePath
	}
	if len(paths) == 1 {
		return paths[0]
	}
	// Walk up from the first file's directory until all paths are beneath it.
	dir := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		for dir != t.SavePath && dir != "/" && dir != "." {
			if strings.HasPrefix(p, dir+string(filepath.Separator)) {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	return dir
}
