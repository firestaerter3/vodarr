package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	vodarrsync "github.com/vodarr/vodarr/internal/sync"
	"github.com/vodarr/vodarr/internal/tmdb"
	"github.com/vodarr/vodarr/internal/xtream"
)

const passwordSentinel = "********"

// Handler serves the web UI backend API and the embedded static frontend.
type Handler struct {
	idx       *index.Index
	scheduler *vodarrsync.Scheduler
	username  string // 2E: optional basic auth for API endpoints
	password  string
	mux       *http.ServeMux

	cfgMu   sync.RWMutex
	cfg     *config.Config
	cfgPath string
}

func NewHandler(idx *index.Index, scheduler *vodarrsync.Scheduler, staticFS fs.FS, cfg *config.Config, cfgPath string, username, password string) *Handler {
	h := &Handler{
		idx:       idx,
		scheduler: scheduler,
		username:  username,
		password:  password,
		mux:       http.NewServeMux(),
		cfg:       cfg,
		cfgPath:   cfgPath,
	}
	h.registerRoutes(staticFS)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 5C: CORS headers for local dev (Vite dev server on different port)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// requireAuth wraps a handler with HTTP Basic Auth when credentials are configured.
func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.username == "" {
			next(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != h.username || p != h.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="VODarr"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handler) registerRoutes(staticFS fs.FS) {
	auth := h.requireAuth

	h.mux.HandleFunc("GET /api/status", auth(h.handleStatus))
	h.mux.HandleFunc("POST /api/sync", auth(h.handleSync))
	h.mux.HandleFunc("GET /api/content/movies", auth(h.handleMovies))
	h.mux.HandleFunc("GET /api/content/series", auth(h.handleSeries))
	h.mux.HandleFunc("GET /api/config", auth(h.handleGetConfig))
	h.mux.HandleFunc("PUT /api/config", auth(h.handlePutConfig))
	h.mux.HandleFunc("POST /api/test-xtream", auth(h.handleTestXtream))
	h.mux.HandleFunc("POST /api/test-tmdb", auth(h.handleTestTMDB))
	h.mux.HandleFunc("GET /api/health", h.handleHealth) // health always public

	// Serve embedded static frontend; fall back to index.html for SPA routing
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try to open the requested path; on error serve index.html (SPA fallback)
			path := r.URL.Path
			if path == "/" {
				path = "index.html"
			} else {
				path = path[1:] // strip leading /
			}
			f, err := staticFS.Open(path)
			if err != nil {
				http.ServeFileFS(w, r, staticFS, "index.html")
				return
			}
			f.Close()
			fileServer.ServeHTTP(w, r)
		})
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, h.scheduler.Status())
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := h.scheduler.Sync(context.Background()); err != nil {
			slog.Error("manual sync failed", "error", err)
		}
	}()
	h.writeJSON(w, map[string]string{"status": "sync started"})
}

func (h *Handler) handleMovies(w http.ResponseWriter, r *http.Request) {
	all := h.idx.All()
	var movies []*index.Item
	for _, item := range all {
		if item.Type == index.TypeMovie {
			movies = append(movies, item)
		}
	}
	if movies == nil {
		movies = []*index.Item{}
	}
	h.writeJSON(w, map[string]interface{}{"items": movies, "total": len(movies)})
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	all := h.idx.All()
	var series []*index.Item
	for _, item := range all {
		if item.Type == index.TypeSeries {
			series = append(series, item)
		}
	}
	if series == nil {
		series = []*index.Item{}
	}
	h.writeJSON(w, map[string]interface{}{"items": series, "total": len(series)})
}

// configResponse is the shape returned by GET /api/config.
type configResponse struct {
	Xtream  xtreamConfigResp  `json:"xtream"`
	TMDB    tmdbConfigResp    `json:"tmdb"`
	Output  outputConfigResp  `json:"output"`
	Sync    syncConfigResp    `json:"sync"`
	Server  serverConfigResp  `json:"server"`
	Logging loggingConfigResp `json:"logging"`
}

type xtreamConfigResp struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type tmdbConfigResp struct {
	APIKey string `json:"api_key"`
}

type outputConfigResp struct {
	Path      string `json:"path"`
	MoviesDir string `json:"movies_dir"`
	SeriesDir string `json:"series_dir"`
}

type syncConfigResp struct {
	Interval  string `json:"interval"`
	OnStartup bool   `json:"on_startup"`
}

type serverConfigResp struct {
	NewznabPort int `json:"newznab_port"`
	QbitPort    int `json:"qbit_port"`
	WebPort     int `json:"web_port"`
}

type loggingConfigResp struct {
	Level string `json:"level"`
}

func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	h.cfgMu.RLock()
	cfg := h.cfg
	h.cfgMu.RUnlock()

	resp := configResponse{
		Xtream: xtreamConfigResp{
			URL:      cfg.Xtream.URL,
			Username: cfg.Xtream.Username,
			Password: maskIfSet(cfg.Xtream.Password),
		},
		TMDB: tmdbConfigResp{
			APIKey: maskIfSet(cfg.TMDB.APIKey),
		},
		Output: outputConfigResp{
			Path:      cfg.Output.Path,
			MoviesDir: cfg.Output.MoviesDir,
			SeriesDir: cfg.Output.SeriesDir,
		},
		Sync: syncConfigResp{
			Interval:  cfg.Sync.Interval,
			OnStartup: cfg.Sync.OnStartup,
		},
		Server: serverConfigResp{
			NewznabPort: cfg.Server.NewznabPort,
			QbitPort:    cfg.Server.QbitPort,
			WebPort:     cfg.Server.WebPort,
		},
		Logging: loggingConfigResp{
			Level: cfg.Logging.Level,
		},
	}
	h.writeJSON(w, resp)
}

// putConfigRequest is the shape of the PUT /api/config body.
type putConfigRequest struct {
	Xtream struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"xtream"`
	TMDB struct {
		APIKey string `json:"api_key"`
	} `json:"tmdb"`
	Output struct {
		Path      string `json:"path"`
		MoviesDir string `json:"movies_dir"`
		SeriesDir string `json:"series_dir"`
	} `json:"output"`
	Sync struct {
		Interval  string `json:"interval"`
		OnStartup bool   `json:"on_startup"`
	} `json:"sync"`
	Server struct {
		NewznabPort int `json:"newznab_port"`
		QbitPort    int `json:"qbit_port"`
		WebPort     int `json:"web_port"`
	} `json:"server"`
	Logging struct {
		Level string `json:"level"`
	} `json:"logging"`
}

func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req putConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()

	// Build new config, resolving sentinel passwords
	newCfg := *h.cfg // shallow copy preserves ServerConfig sensitive fields

	newCfg.Xtream.URL = req.Xtream.URL
	newCfg.Xtream.Username = req.Xtream.Username
	newCfg.Xtream.Password = resolveSentinel(req.Xtream.Password, h.cfg.Xtream.Password)
	newCfg.TMDB.APIKey = resolveSentinel(req.TMDB.APIKey, h.cfg.TMDB.APIKey)
	newCfg.Output.Path = req.Output.Path
	newCfg.Output.MoviesDir = req.Output.MoviesDir
	newCfg.Output.SeriesDir = req.Output.SeriesDir
	newCfg.Sync.Interval = req.Sync.Interval
	newCfg.Sync.OnStartup = req.Sync.OnStartup
	newCfg.Logging.Level = req.Logging.Level

	// Only override the three public port fields; preserve all other ServerConfig fields
	newCfg.Server = h.cfg.Server
	if req.Server.NewznabPort != 0 {
		newCfg.Server.NewznabPort = req.Server.NewznabPort
	}
	if req.Server.QbitPort != 0 {
		newCfg.Server.QbitPort = req.Server.QbitPort
	}
	if req.Server.WebPort != 0 {
		newCfg.Server.WebPort = req.Server.WebPort
	}

	if err := newCfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := config.Save(h.cfgPath, &newCfg); err != nil {
		slog.Error("failed to save config", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to save config: " + err.Error()})
		return
	}

	h.cfg = &newCfg
	h.writeJSON(w, map[string]interface{}{"restart_required": true})
}

type testXtreamRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) handleTestXtream(w http.ResponseWriter, r *http.Request) {
	var req testXtreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedPassword := h.cfg.Xtream.Password
	h.cfgMu.RUnlock()

	password := resolveSentinel(req.Password, storedPassword)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	xc := xtream.NewClient(req.URL, req.Username, password)
	info, err := xc.Authenticate(ctx)
	if err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	h.writeJSON(w, map[string]interface{}{
		"success": true,
		"user":    info.UserInfo.Username,
		"status":  info.UserInfo.Status,
	})
}

type testTMDBRequest struct {
	APIKey string `json:"api_key"`
}

func (h *Handler) handleTestTMDB(w http.ResponseWriter, r *http.Request) {
	var req testTMDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedKey := h.cfg.TMDB.APIKey
	h.cfgMu.RUnlock()

	apiKey := resolveSentinel(req.APIKey, storedKey)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tc := tmdb.NewClient(apiKey)
	defer tc.Stop()

	if err := tc.Validate(ctx); err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	h.writeJSON(w, map[string]interface{}{"success": true})
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json encode error", "error", err)
	}
}

// maskIfSet returns the sentinel string if s is non-empty, otherwise "".
func maskIfSet(s string) string {
	if s != "" {
		return passwordSentinel
	}
	return ""
}

// resolveSentinel returns stored if incoming is the sentinel or empty.
func resolveSentinel(incoming, stored string) string {
	if incoming == passwordSentinel || incoming == "" {
		return stored
	}
	return incoming
}
