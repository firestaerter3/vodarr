package qbit

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/vodarr/vodarr/internal/strm"
	"github.com/vodarr/vodarr/internal/xtream"
)

// Handler impersonates the qBittorrent Web API v2.
type Handler struct {
	store    *Store
	writer   *strm.Writer
	xtream   *xtream.Client
	savePath string
	mux      *http.ServeMux
}

func NewHandler(store *Store, writer *strm.Writer, xc *xtream.Client, savePath string) *Handler {
	h := &Handler{
		store:    store,
		writer:   writer,
		xtream:   xc,
		savePath: savePath,
		mux:      http.NewServeMux(),
	}
	h.registerRoutes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	// Auth
	h.mux.HandleFunc("POST /api/v2/auth/login", h.handleLogin)

	// App info
	h.mux.HandleFunc("GET /api/v2/app/version", h.handleAppVersion)
	h.mux.HandleFunc("GET /api/v2/app/webapiVersion", h.handleWebapiVersion)
	h.mux.HandleFunc("GET /api/v2/app/preferences", h.handlePreferences)
	h.mux.HandleFunc("GET /api/v2/app/buildInfo", h.handleBuildInfo)

	// Torrents
	h.mux.HandleFunc("POST /api/v2/torrents/add", h.handleTorrentsAdd)
	h.mux.HandleFunc("GET /api/v2/torrents/info", h.handleTorrentsInfo)
	h.mux.HandleFunc("GET /api/v2/torrents/properties", h.handleTorrentsProperties)
	h.mux.HandleFunc("GET /api/v2/torrents/files", h.handleTorrentsFiles)
	h.mux.HandleFunc("POST /api/v2/torrents/delete", h.handleTorrentsDelete)
	h.mux.HandleFunc("POST /api/v2/torrents/pause", h.handleStub)
	h.mux.HandleFunc("POST /api/v2/torrents/resume", h.handleStub)
	h.mux.HandleFunc("GET /api/v2/torrents/categories", h.handleCategories)
	h.mux.HandleFunc("POST /api/v2/torrents/createCategory", h.handleStub)
	h.mux.HandleFunc("POST /api/v2/torrents/setCategory", h.handleStub)
	h.mux.HandleFunc("POST /api/v2/torrents/setSavePath", h.handleStub)

	// Sync / transfer
	h.mux.HandleFunc("GET /api/v2/sync/maindata", h.handleSyncMaindata)
	h.mux.HandleFunc("GET /api/v2/transfer/info", h.handleTransferInfo)
}

// ---- Auth ----

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:  "SID",
		Value: "vodarrsid",
		Path:  "/",
	})
	w.Write([]byte("Ok."))
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

	// Get the URL(s) from the form — Sonarr/Radarr send "urls" field
	urls := r.FormValue("urls")
	if urls == "" {
		urls = r.FormValue("url")
	}

	savePath := r.FormValue("savepath")
	if savePath == "" {
		savePath = h.savePath
	}

	category := r.FormValue("category")
	_ = category

	if urls == "" {
		slog.Warn("torrents/add: no urls provided")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Each URL is on its own line
	for _, rawURL := range strings.Split(strings.TrimSpace(urls), "\n") {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		if err := h.processURL(rawURL, savePath); err != nil {
			slog.Error("torrents/add: processing failed", "url", rawURL, "error", err)
			http.Error(w, "Fails.", http.StatusInternalServerError)
			return
		}
	}

	w.Write([]byte("Ok."))
}

func (h *Handler) processURL(rawURL, savePath string) error {
	// The URL is the Newznab t=get URL which returns a JSON descriptor
	resp, err := http.Get(rawURL)
	if err != nil {
		return fmt.Errorf("fetch descriptor: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read descriptor: %w", err)
	}

	var desc struct {
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
	if err := json.Unmarshal(body, &desc); err != nil {
		return fmt.Errorf("parse descriptor: %w", err)
	}

	hash := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d", desc.Type, desc.XtreamID))))

	t := &Torrent{
		Hash:         hash,
		Name:         desc.Name,
		SavePath:     savePath,
		State:        StateUploading,
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

	// Create STRM file(s) asynchronously
	go func() {
		var strmPaths []string
		var writeErr error

		ext := desc.ContainerExt
		if ext == "" {
			ext = "mkv"
		}

		switch desc.Type {
		case "movie":
			streamURL := h.xtream.StreamURL(desc.XtreamID, ext)
			path, err := h.writer.WriteMovie(desc.Name, desc.Year, streamURL)
			if err != nil {
				writeErr = err
			} else {
				strmPaths = append(strmPaths, path)
			}

		case "series":
			for _, ep := range desc.Episodes {
				epExt := ep.Ext
				if epExt == "" {
					epExt = "mkv"
				}
				streamURL := h.xtream.SeriesStreamURL(ep.EpisodeID, epExt)
				path, err := h.writer.WriteEpisode(desc.Name, ep.Season, ep.EpisodeNum, ep.Title, streamURL)
				if err != nil {
					slog.Warn("strm write episode failed", "error", err)
					continue
				}
				strmPaths = append(strmPaths, path)
			}
		}

		if writeErr != nil {
			slog.Error("strm write failed", "name", desc.Name, "error", writeErr)
		}

		h.store.SetComplete(hash, strmPaths)
		slog.Info("strm created", "name", desc.Name, "type", desc.Type, "files", len(strmPaths))
	}()

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
		contentPath := t.SavePath
		if len(t.StrmPaths) > 0 {
			contentPath = t.StrmPaths[0]
		}
		out = append(out, qbTorrent{
			Hash:         t.Hash,
			Name:         t.Name,
			Size:         t.Size,
			TotalSize:    t.Size,
			Progress:     t.Progress,
			State:        t.State,
			SavePath:     t.SavePath,
			ContentPath:  contentPath,
			AddedOn:      t.AddedOn,
			CompletionOn: t.CompletionOn,
			Ratio:        1.0,
			Eta:          0,
			AmountLeft:   0,
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
		"created_by":        "Vodarr",
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

	var files []fileEntry
	for i, p := range t.StrmPaths {
		files = append(files, fileEntry{
			Index:        i,
			Name:         p,
			Size:         64,
			Progress:     1.0,
			Priority:     1,
			IsSeed:       true,
			PieceRange:   []int{0, 0},
			Availability: 1.0,
		})
	}
	if len(files) == 0 {
		// Fallback before strm is written
		files = append(files, fileEntry{
			Index:        0,
			Name:         t.Name + ".strm",
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
	for _, hash := range strings.Split(hashes, "|") {
		hash = strings.TrimSpace(hash)
		if hash != "" {
			h.store.Delete(hash)
		}
	}
	w.Write([]byte("Ok."))
}

func (h *Handler) handleCategories(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]interface{}{})
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
