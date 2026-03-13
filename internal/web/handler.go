package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	vodarrsync "github.com/vodarr/vodarr/internal/sync"
	"github.com/vodarr/vodarr/internal/strm"
	"github.com/vodarr/vodarr/internal/tmdb"
	"github.com/vodarr/vodarr/internal/tvdb"
	"github.com/vodarr/vodarr/internal/xtream"
)

const passwordSentinel = "********"

// Handler serves the web UI backend API and the embedded static frontend.
type Handler struct {
	idx       *index.Index
	scheduler *vodarrsync.Scheduler
	writer    *strm.Writer // nil = strm refresh disabled
	version   string
	username  string // 2E: optional basic auth for API endpoints
	password  string
	mux       *http.ServeMux

	cfgMu      sync.RWMutex
	cfg        *config.Config
	cfgPath    string
	refreshing atomic.Bool // guards against concurrent /api/strm/refresh calls
}

func NewHandler(idx *index.Index, scheduler *vodarrsync.Scheduler, writer *strm.Writer, staticFS fs.FS, cfg *config.Config, cfgPath string, username, password, version string) *Handler {
	h := &Handler{
		idx:       idx,
		scheduler: scheduler,
		writer:    writer,
		version:   version,
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
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
		usernameOK := subtle.ConstantTimeCompare([]byte(u), []byte(h.username)) == 1
		passwordOK := subtle.ConstantTimeCompare([]byte(p), []byte(h.password)) == 1
		if !ok || !usernameOK || !passwordOK {
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
	h.mux.HandleFunc("POST /api/test-tvdb", auth(h.handleTestTVDB))
	h.mux.HandleFunc("POST /api/restart", auth(h.handleRestart))
	h.mux.HandleFunc("GET /api/health", h.handleHealth)    // health always public
	h.mux.HandleFunc("POST /api/webhook", h.handleWebhook) // webhook always public (called by arr)
	h.mux.HandleFunc("POST /api/arr/test", auth(h.handleArrTest))
	h.mux.HandleFunc("GET /api/arr/status", auth(h.handleArrStatus))
	h.mux.HandleFunc("POST /api/arr/setup", auth(h.handleArrSetup))
	h.mux.HandleFunc("POST /api/strm/refresh", auth(h.handleStrmRefresh))
	h.mux.HandleFunc("GET /api/sync/history", auth(h.handleSyncHistory))

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
	status := h.scheduler.Status()
	type statusWithVersion struct {
		vodarrsync.Status
		Version string `json:"version"`
	}
	h.writeJSON(w, statusWithVersion{Status: status, Version: h.version})
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
	statusFilter := r.URL.Query().Get("status")
	all := h.idx.All()
	var movies []*index.Item
	for _, item := range all {
		if item.Type != index.TypeMovie {
			continue
		}
		if !matchesStatusFilter(item, statusFilter) {
			continue
		}
		movies = append(movies, item)
	}
	if movies == nil {
		movies = []*index.Item{}
	}
	h.writeJSON(w, map[string]interface{}{"items": movies, "total": len(movies)})
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	all := h.idx.All()
	var series []*index.Item
	for _, item := range all {
		if item.Type != index.TypeSeries {
			continue
		}
		if !matchesStatusFilter(item, statusFilter) {
			continue
		}
		series = append(series, item)
	}
	if series == nil {
		series = []*index.Item{}
	}
	h.writeJSON(w, map[string]interface{}{"items": series, "total": len(series)})
}

// matchesStatusFilter returns true when item passes the given status filter.
// Supported values: "unenriched", "grace". Empty or unknown = pass everything.
func matchesStatusFilter(item *index.Item, filter string) bool {
	switch filter {
	case "unenriched":
		return item.IMDBId == "" && item.TVDBId == ""
	case "grace":
		return item.MissingSince > 0
	default:
		return true
	}
}

// configResponse is the shape returned by GET /api/config.
type configResponse struct {
	Xtream  xtreamConfigResp  `json:"xtream"`
	TMDB    tmdbConfigResp    `json:"tmdb"`
	Output  outputConfigResp  `json:"output"`
	Sync    syncConfigResp    `json:"sync"`
	Server  serverConfigResp  `json:"server"`
	Logging loggingConfigResp `json:"logging"`
	Arr     arrConfigResp     `json:"arr"`
}

type arrConfigResp struct {
	Instances []arrInstanceResp `json:"instances"`
}

type arrInstanceResp struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

type xtreamConfigResp struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type tmdbConfigResp struct {
	APIKey     string `json:"api_key"`
	TVDBAPIKey string `json:"tvdb_api_key"`
}

type outputConfigResp struct {
	Path      string `json:"path"`
	MoviesDir string `json:"movies_dir"`
	SeriesDir string `json:"series_dir"`
}

type syncConfigResp struct {
	Interval             string   `json:"interval"`
	OnStartup            bool     `json:"on_startup"`
	Parallelism          int      `json:"parallelism"`
	TitleCleanupPatterns []string `json:"title_cleanup_patterns"`
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
			APIKey:     maskIfSet(cfg.TMDB.APIKey),
			TVDBAPIKey: maskIfSet(cfg.TMDB.TVDBAPIKey),
		},
		Output: outputConfigResp{
			Path:      cfg.Output.Path,
			MoviesDir: cfg.Output.MoviesDir,
			SeriesDir: cfg.Output.SeriesDir,
		},
		Sync: syncConfigResp{
			Interval:             cfg.Sync.Interval,
			OnStartup:            cfg.Sync.OnStartup,
			Parallelism:          cfg.Sync.Parallelism,
			TitleCleanupPatterns: cfg.Sync.TitleCleanupPatterns,
		},
		Server: serverConfigResp{
			NewznabPort: cfg.Server.NewznabPort,
			QbitPort:    cfg.Server.QbitPort,
			WebPort:     cfg.Server.WebPort,
		},
		Logging: loggingConfigResp{
			Level: cfg.Logging.Level,
		},
		Arr: func() arrConfigResp {
			instances := make([]arrInstanceResp, len(cfg.Arr.Instances))
			for i, inst := range cfg.Arr.Instances {
				instances[i] = arrInstanceResp{
					Name:   inst.Name,
					Type:   inst.Type,
					URL:    inst.URL,
					APIKey: maskIfSet(inst.APIKey),
				}
			}
			return arrConfigResp{Instances: instances}
		}(),
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
		APIKey     string `json:"api_key"`
		TVDBAPIKey string `json:"tvdb_api_key"`
	} `json:"tmdb"`
	Output struct {
		Path      string `json:"path"`
		MoviesDir string `json:"movies_dir"`
		SeriesDir string `json:"series_dir"`
	} `json:"output"`
	Sync struct {
		Interval             string   `json:"interval"`
		OnStartup            bool     `json:"on_startup"`
		Parallelism          int      `json:"parallelism"`
		TitleCleanupPatterns []string `json:"title_cleanup_patterns"`
	} `json:"sync"`
	Server struct {
		NewznabPort int `json:"newznab_port"`
		QbitPort    int `json:"qbit_port"`
		WebPort     int `json:"web_port"`
	} `json:"server"`
	Logging struct {
		Level string `json:"level"`
	} `json:"logging"`
	Arr struct {
		Instances []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			URL    string `json:"url"`
			APIKey string `json:"api_key"`
		} `json:"instances"`
	} `json:"arr"`
}

func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
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
	newCfg.TMDB.TVDBAPIKey = resolveSentinel(req.TMDB.TVDBAPIKey, h.cfg.TMDB.TVDBAPIKey)
	newCfg.Output.Path = req.Output.Path
	newCfg.Output.MoviesDir = req.Output.MoviesDir
	newCfg.Output.SeriesDir = req.Output.SeriesDir
	newCfg.Sync.Interval = req.Sync.Interval
	newCfg.Sync.OnStartup = req.Sync.OnStartup
	if req.Sync.Parallelism != 0 {
		newCfg.Sync.Parallelism = req.Sync.Parallelism
	}
	// Filter empty patterns before saving
	var cleanPatterns []string
	for _, p := range req.Sync.TitleCleanupPatterns {
		if p = strings.TrimSpace(p); p != "" {
			cleanPatterns = append(cleanPatterns, p)
		}
	}
	newCfg.Sync.TitleCleanupPatterns = cleanPatterns
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

	// Apply arr instances, resolving sentinel API keys against stored values
	newInstances := make([]config.ArrInstance, len(req.Arr.Instances))
	for i, ri := range req.Arr.Instances {
		// Find stored instance with same name to resolve sentinel
		storedKey := ""
		for _, si := range h.cfg.Arr.Instances {
			if si.Name == ri.Name {
				storedKey = si.APIKey
				break
			}
		}
		newInstances[i] = config.ArrInstance{
			Name:   ri.Name,
			Type:   ri.Type,
			URL:    ri.URL,
			APIKey: resolveSentinel(ri.APIKey, storedKey),
		}
	}
	newCfg.Arr.Instances = newInstances

	if err := newCfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := config.CheckWritable(newCfg.Output.Path); err != nil {
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
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var req testXtreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedPassword := h.cfg.Xtream.Password
	h.cfgMu.RUnlock()

	password := resolveSentinel(req.Password, storedPassword)

	if err := validateURLScheme(req.URL); err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": "invalid URL: " + err.Error()})
		return
	}

	// Use Background context so Brave/HTTP2 connection cancellation doesn't abort the outbound request
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var req testTMDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedKey := h.cfg.TMDB.APIKey
	h.cfgMu.RUnlock()

	apiKey := resolveSentinel(req.APIKey, storedKey)

	// Use Background context so Brave/HTTP2 connection cancellation doesn't abort the outbound request
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tc := tmdb.NewClient(apiKey)
	defer tc.Stop()

	if err := tc.Validate(ctx); err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	h.writeJSON(w, map[string]interface{}{"success": true})
}

type testTVDBRequest struct {
	TVDBAPIKey string `json:"tvdb_api_key"`
}

func (h *Handler) handleTestTVDB(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var req testTVDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedKey := h.cfg.TMDB.TVDBAPIKey
	h.cfgMu.RUnlock()

	apiKey := resolveSentinel(req.TVDBAPIKey, storedKey)
	if apiKey == "" {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": "no TVDB API key configured"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tc := tvdb.NewClient(apiKey)
	if _, err := tc.EnsureToken(ctx); err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	h.writeJSON(w, map[string]interface{}{"success": true})
}

func (h *Handler) handleArrTest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var req struct {
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	storedKey := ""
	for _, inst := range h.cfg.Arr.Instances {
		if inst.URL == req.URL {
			storedKey = inst.APIKey
			break
		}
	}
	h.cfgMu.RUnlock()

	apiKey := resolveSentinel(req.APIKey, storedKey)

	if err := validateURLScheme(req.URL); err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": "invalid URL: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testURL := fmt.Sprintf("%s/api/v3/system/status", strings.TrimRight(req.URL, "/"))
	testReq, _ := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	testReq.Header.Set("X-Api-Key", apiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(testReq)
	if err != nil {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": "invalid API key"})
		return
	}
	if resp.StatusCode >= 300 {
		h.writeJSON(w, map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}
	var status struct {
		AppName string `json:"appName"`
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&status)
	h.writeJSON(w, map[string]interface{}{"success": true, "app": status.AppName, "version": status.Version})
}

func (h *Handler) handleRestart(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(150 * time.Millisecond) // let the response flush
		slog.Info("restarting via API request")
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
}

// handleWebhook handles POST /api/webhook — called by Sonarr/Radarr after import.
// Deletes the .mkv stub when a matching .strm sibling exists.
// Always returns 200 (arr retries on non-2xx).
func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var payload struct {
		EventType   string `json:"eventType"`
		EpisodeFile struct {
			Path string `json:"path"`
		} `json:"episodeFile"`
		MovieFile struct {
			Path string `json:"path"`
		} `json:"movieFile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		// Return 200 even on parse errors so arr doesn't retry
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// Test event from arr when adding the webhook — just acknowledge
	if payload.EventType == "Test" {
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// Only act on Download events
	if payload.EventType != "Download" {
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// Determine which path field was populated
	mkvPath := payload.EpisodeFile.Path
	if mkvPath == "" {
		mkvPath = payload.MovieFile.Path
	}
	if mkvPath == "" || !strings.HasSuffix(mkvPath, ".mkv") {
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// Safety: path must be absolute
	if !filepath.IsAbs(mkvPath) {
		slog.Warn("webhook: received non-absolute path, ignoring", "path", mkvPath)
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// Safety: path must be contained within the configured output directory
	h.cfgMu.RLock()
	outputPath := h.cfg.Output.Path
	h.cfgMu.RUnlock()
	if outputPath != "" {
		absPath, _ := filepath.Abs(mkvPath)
		absOutput, _ := filepath.Abs(outputPath)
		sep := string(filepath.Separator)
		if !strings.HasPrefix(absPath+sep, absOutput+sep) {
			slog.Warn("webhook: path outside output directory, ignoring", "path", mkvPath)
			h.writeJSON(w, map[string]string{"status": "ok"})
			return
		}
	}

	// Only delete if a .strm sibling exists (confirms VODarr managed this download)
	strmPath := strings.TrimSuffix(mkvPath, ".mkv") + ".strm"
	if _, err := os.Stat(strmPath); os.IsNotExist(err) {
		slog.Debug("webhook: no .strm sibling, skipping delete", "mkv", mkvPath)
		h.writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	if err := os.Remove(mkvPath); err != nil {
		slog.Error("webhook: failed to remove mkv stub", "path", mkvPath, "error", err)
	} else {
		slog.Info("webhook: removed mkv stub after import", "path", mkvPath)
	}
	h.writeJSON(w, map[string]string{"status": "ok"})
}

// arrInstanceStatus is the per-instance result from GET /api/arr/status.
type arrInstanceStatus struct {
	Name                     string   `json:"name"`
	Type                     string   `json:"type"`
	Reachable                bool     `json:"reachable"`
	ImportExtraFiles         bool     `json:"importExtraFiles"`
	ExtraFileExts            string   `json:"extraFileExtensions"`
	WebhookConfigured        bool     `json:"webhookConfigured"`
	IndexerConfigured        bool     `json:"indexerConfigured"`
	DownloadClientConfigured bool     `json:"downloadClientConfigured"`
	Issues                   []string `json:"issues"`
}

func (h *Handler) handleArrStatus(w http.ResponseWriter, r *http.Request) {
	h.cfgMu.RLock()
	cfg := h.cfg
	h.cfgMu.RUnlock()

	webhookURL := h.webhookURL(r)
	results := make([]arrInstanceStatus, 0, len(cfg.Arr.Instances))
	for _, inst := range cfg.Arr.Instances {
		results = append(results, h.checkArrInstance(r.Context(), inst, webhookURL, cfg.Server.NewznabPort, cfg.Server.QbitPort))
	}
	h.writeJSON(w, map[string]interface{}{"instances": results})
}

func (h *Handler) checkArrInstance(ctx context.Context, inst config.ArrInstance, webhookURL string, newznabPort, qbitPort int) arrInstanceStatus {
	st := arrInstanceStatus{Name: inst.Name, Type: inst.Type, Issues: []string{}}

	// Check media management settings
	mmURL := fmt.Sprintf("%s/api/v3/config/mediamanagement", strings.TrimRight(inst.URL, "/"))
	mmReq, _ := http.NewRequestWithContext(ctx, "GET", mmURL, nil)
	mmReq.Header.Set("X-Api-Key", inst.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	mmResp, err := client.Do(mmReq)
	if err != nil {
		st.Issues = append(st.Issues, "unreachable: "+err.Error())
		return st
	}
	defer mmResp.Body.Close()
	st.Reachable = true

	var mm struct {
		ImportExtraFiles    bool   `json:"importExtraFiles"`
		ExtraFileExtensions string `json:"extraFileExtensions"`
	}
	if err := json.NewDecoder(mmResp.Body).Decode(&mm); err == nil {
		st.ImportExtraFiles = mm.ImportExtraFiles
		st.ExtraFileExts = mm.ExtraFileExtensions
		if !mm.ImportExtraFiles {
			st.Issues = append(st.Issues, "importExtraFiles not enabled")
		}
		if !strings.Contains(mm.ExtraFileExtensions, "strm") {
			st.Issues = append(st.Issues, "extraFileExtensions does not include strm")
		}
	}

	// Check notifications for webhook
	if webhookURL != "" {
		notifURL := fmt.Sprintf("%s/api/v3/notification", strings.TrimRight(inst.URL, "/"))
		notifReq, _ := http.NewRequestWithContext(ctx, "GET", notifURL, nil)
		notifReq.Header.Set("X-Api-Key", inst.APIKey)
		notifResp, err := client.Do(notifReq)
		if err == nil {
			defer notifResp.Body.Close()
			var notifications []struct {
				Implementation string `json:"implementation"`
				Fields         []struct {
					Name  string      `json:"name"`
					Value interface{} `json:"value"`
				} `json:"fields"`
				OnDownload bool `json:"onDownload"`
			}
			if json.NewDecoder(notifResp.Body).Decode(&notifications) == nil {
				for _, n := range notifications {
					if n.Implementation != "Webhook" {
						continue
					}
					for _, f := range n.Fields {
						if f.Name == "url" {
							if url, ok := f.Value.(string); ok && strings.Contains(url, "/api/webhook") && n.OnDownload {
								st.WebhookConfigured = true
							}
						}
					}
				}
			}
		}
		if !st.WebhookConfigured {
			st.Issues = append(st.Issues, "webhook Connection not configured")
		}
	}

	// Check indexer registration
	idxURL := fmt.Sprintf("%s/api/v3/indexer", strings.TrimRight(inst.URL, "/"))
	idxReq, _ := http.NewRequestWithContext(ctx, "GET", idxURL, nil)
	idxReq.Header.Set("X-Api-Key", inst.APIKey)
	if idxResp, err := client.Do(idxReq); err == nil {
		defer idxResp.Body.Close()
		var indexers []struct {
			Implementation string `json:"implementation"`
			Fields         []struct {
				Name  string      `json:"name"`
				Value interface{} `json:"value"`
			} `json:"fields"`
		}
		portStr := fmt.Sprintf(":%d", newznabPort)
		if json.NewDecoder(idxResp.Body).Decode(&indexers) == nil {
			for _, idx := range indexers {
				if idx.Implementation != "Newznab" && idx.Implementation != "Torznab" {
					continue
				}
				for _, f := range idx.Fields {
					if f.Name == "baseUrl" {
						if u, ok := f.Value.(string); ok && strings.Contains(u, portStr) {
							st.IndexerConfigured = true
						}
					}
				}
			}
		}
	}

	// Check download client registration
	dcURL := fmt.Sprintf("%s/api/v3/downloadclient", strings.TrimRight(inst.URL, "/"))
	dcReq, _ := http.NewRequestWithContext(ctx, "GET", dcURL, nil)
	dcReq.Header.Set("X-Api-Key", inst.APIKey)
	if dcResp, err := client.Do(dcReq); err == nil {
		defer dcResp.Body.Close()
		var dcs []struct {
			Implementation string `json:"implementation"`
			Fields         []struct {
				Name  string      `json:"name"`
				Value interface{} `json:"value"`
			} `json:"fields"`
		}
		if json.NewDecoder(dcResp.Body).Decode(&dcs) == nil {
			for _, dc := range dcs {
				if dc.Implementation != "QBittorrent" {
					continue
				}
				for _, f := range dc.Fields {
					if f.Name == "port" {
						if v, ok := f.Value.(float64); ok && int(v) == qbitPort {
							st.DownloadClientConfigured = true
						}
					}
				}
			}
		}
	}

	return st
}

type arrSetupRequest struct {
	Instance string `json:"instance"`
}

func (h *Handler) handleArrSetup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB
	var req arrSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.cfgMu.RLock()
	cfg := h.cfg
	h.cfgMu.RUnlock()

	var inst *config.ArrInstance
	for i := range cfg.Arr.Instances {
		if cfg.Arr.Instances[i].Name == req.Instance {
			inst = &cfg.Arr.Instances[i]
			break
		}
	}
	if inst == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}

	if err := validateURLScheme(inst.URL); err != nil {
		http.Error(w, `{"error":"instance URL is invalid: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	webhookURL := h.webhookURL(r)

	const (
		arrSetupTimeout = 90 * time.Second
		arrSetupRetries = 1
	)

	results := map[string]interface{}{}
	client := &http.Client{Timeout: arrSetupTimeout}
	baseURL := strings.TrimRight(inst.URL, "/")
	doWithRetry := func(method, url string, body []byte) (*http.Response, error) {
		var lastErr error
		for attempt := 0; attempt <= arrSetupRetries; attempt++ {
			var bodyReader io.Reader
			if len(body) > 0 {
				bodyReader = bytes.NewReader(body)
			}
			req, _ := http.NewRequestWithContext(r.Context(), method, url, bodyReader)
			req.Header.Set("X-Api-Key", inst.APIKey)
			if len(body) > 0 {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := client.Do(req)
			if err == nil {
				return resp, nil
			}
			lastErr = err
			var netErr net.Error
			isTimeout := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
			if !isTimeout || attempt == arrSetupRetries {
				return nil, err
			}
			slog.Warn("arr setup request timed out, retrying", "instance", inst.Name, "attempt", attempt+1, "method", method, "url", url)
		}
		return nil, lastErr
	}

	// Step 1: Configure importExtraFiles
	mmURL := fmt.Sprintf("%s/api/v3/config/mediamanagement", baseURL)
	getReq, _ := http.NewRequestWithContext(r.Context(), "GET", mmURL, nil)
	getReq.Header.Set("X-Api-Key", inst.APIKey)
	getResp, err := client.Do(getReq)
	if err != nil {
		results["importExtraFiles"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		defer getResp.Body.Close()
		var mm map[string]interface{}
		if err := json.NewDecoder(getResp.Body).Decode(&mm); err == nil {
			mm["importExtraFiles"] = true
			// Append strm to extraFileExtensions if not already there
			exts, _ := mm["extraFileExtensions"].(string)
			if !strings.Contains(exts, "strm") {
				if exts == "" {
					mm["extraFileExtensions"] = "strm"
				} else {
					mm["extraFileExtensions"] = exts + ",strm"
				}
			}
			body, _ := json.Marshal(mm)
			putReq, _ := http.NewRequestWithContext(r.Context(), "PUT", mmURL, bytes.NewReader(body))
			putReq.Header.Set("X-Api-Key", inst.APIKey)
			putReq.Header.Set("Content-Type", "application/json")
			putResp, err := client.Do(putReq)
			if err != nil {
				results["importExtraFiles"] = map[string]interface{}{"success": false, "error": err.Error()}
			} else {
				putResp.Body.Close()
				results["importExtraFiles"] = map[string]interface{}{"success": putResp.StatusCode < 300}
			}
		} else {
			results["importExtraFiles"] = map[string]interface{}{"success": false, "error": "failed to parse mediamanagement response"}
		}
	}

	// Step 2: Look up or create vodarr tag
	tagID := h.ensureTag(r.Context(), client, baseURL, inst.APIKey, "vodarr")

	// Step 3: Create webhook notification if not already present
	notifURL := fmt.Sprintf("%s/api/v3/notification", baseURL)
	listReq, _ := http.NewRequestWithContext(r.Context(), "GET", notifURL, nil)
	listReq.Header.Set("X-Api-Key", inst.APIKey)
	listResp, err := client.Do(listReq)
	if err != nil {
		results["webhook"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		defer listResp.Body.Close()
		var notifications []struct {
			Implementation string `json:"implementation"`
			Fields         []struct {
				Name  string      `json:"name"`
				Value interface{} `json:"value"`
			} `json:"fields"`
			OnDownload bool `json:"onDownload"`
		}
		alreadyExists := false
		if json.NewDecoder(listResp.Body).Decode(&notifications) == nil {
			for _, n := range notifications {
				if n.Implementation != "Webhook" {
					continue
				}
				for _, f := range n.Fields {
					if f.Name == "url" {
						if url, ok := f.Value.(string); ok && strings.Contains(url, "/api/webhook") {
							alreadyExists = true
						}
					}
				}
			}
		}
		if alreadyExists {
			results["webhook"] = map[string]interface{}{"success": true, "skipped": "already configured"}
		} else {
			tags := []int{}
			if tagID >= 0 {
				tags = []int{tagID}
			}
			notif := map[string]interface{}{
				"name":                                  "VODarr Cleanup",
				"implementation":                        "Webhook",
				"implementationName":                    "Webhook",
				"configContract":                        "WebhookSettings",
				"onGrab":                                false,
				"onDownload":                            true,
				"onUpgrade":                             true,
				"onRename":                              false,
				"onSeriesAdd":                           false,
				"onSeriesDelete":                        false,
				"onEpisodeFileDelete":                   false,
				"onEpisodeFileDeleteForUpgrade":         false,
				"onHealthIssue":                         false,
				"onHealthRestored":                      false,
				"onApplicationUpdate":                   false,
				"onManualInteractionRequired":           false,
				"supportsOnGrab":                        false,
				"supportsOnDownload":                    true,
				"supportsOnUpgrade":                     true,
				"supportsOnRename":                      false,
				"supportsOnSeriesAdd":                   false,
				"supportsOnSeriesDelete":                false,
				"supportsOnEpisodeFileDelete":           false,
				"supportsOnEpisodeFileDeleteForUpgrade": false,
				"supportsOnHealthIssue":                 false,
				"supportsOnHealthRestored":              false,
				"supportsOnApplicationUpdate":           false,
				"supportsOnManualInteractionRequired":   false,
				"includeHealthWarnings":                 false,
				"tags":                                  tags,
				"fields": []map[string]interface{}{
					{"name": "url", "value": webhookURL + "/api/webhook"},
					{"name": "method", "value": 1},
					{"name": "username", "value": ""},
					{"name": "password", "value": ""},
					{"name": "headers", "value": []interface{}{}},
				},
			}
			body, _ := json.Marshal(notif)
			postReq, _ := http.NewRequestWithContext(r.Context(), "POST", notifURL, bytes.NewReader(body))
			postReq.Header.Set("X-Api-Key", inst.APIKey)
			postReq.Header.Set("Content-Type", "application/json")
			postResp, err := client.Do(postReq)
			if err != nil {
				results["webhook"] = map[string]interface{}{"success": false, "error": err.Error()}
			} else {
				respBody, _ := io.ReadAll(postResp.Body)
				postResp.Body.Close()
				if postResp.StatusCode < 300 {
					results["webhook"] = map[string]interface{}{"success": true}
				} else {
					results["webhook"] = map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d: %s", postResp.StatusCode, strings.TrimSpace(string(respBody)))}
				}
			}
		}
	}

	// Step 4: Register indexer
	indexerListURL := fmt.Sprintf("%s/api/v3/indexer", baseURL)
	idxListReq, _ := http.NewRequestWithContext(r.Context(), "GET", indexerListURL, nil)
	idxListReq.Header.Set("X-Api-Key", inst.APIKey)
	idxListResp, err := client.Do(idxListReq)
	if err != nil {
		results["indexer"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		defer idxListResp.Body.Close()
		var indexers []struct {
			Implementation string `json:"implementation"`
			Fields         []struct {
				Name  string      `json:"name"`
				Value interface{} `json:"value"`
			} `json:"fields"`
		}
		newznabURL := h.newznabBaseURL(r, cfg.Server.NewznabPort)
		portStr := fmt.Sprintf(":%d", cfg.Server.NewznabPort)
		indexerExists := false
		if json.NewDecoder(idxListResp.Body).Decode(&indexers) == nil {
			for _, idx := range indexers {
				if idx.Implementation != "Newznab" && idx.Implementation != "Torznab" {
					continue
				}
				for _, f := range idx.Fields {
					if f.Name == "baseUrl" {
						if u, ok := f.Value.(string); ok && strings.Contains(u, portStr) {
							indexerExists = true
						}
					}
				}
			}
		}
		if indexerExists {
			results["indexer"] = map[string]interface{}{"success": true, "skipped": "already configured"}
		} else {
			categories := []int{5000}
			if inst.Type == "radarr" {
				categories = []int{2000}
			}
			indexer := map[string]interface{}{
				"name":                    "VODarr",
				"implementation":          "Torznab",
				"implementationName":      "Torznab",
				"configContract":          "TorznabSettings",
				"enableRss":               true,
				"enableAutomaticSearch":   true,
				"enableInteractiveSearch": true,
				"supportsRss":             true,
				"supportsSearch":          true,
				"priority":                1,
				"tags":                    []int{},
				"fields": []map[string]interface{}{
					{"name": "baseUrl", "value": newznabURL},
					{"name": "apiPath", "value": "/api"},
					{"name": "apiKey", "value": ""},
					{"name": "categories", "value": categories},
				},
			}
			idxBody, _ := json.Marshal(indexer)
			idxPostResp, err := doWithRetry(http.MethodPost, indexerListURL, idxBody)
			if err != nil {
				results["indexer"] = map[string]interface{}{"success": false, "error": err.Error()}
			} else {
				idxRespBody, _ := io.ReadAll(idxPostResp.Body)
				idxPostResp.Body.Close()
				if idxPostResp.StatusCode < 300 {
					results["indexer"] = map[string]interface{}{"success": true}
				} else {
					results["indexer"] = map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d: %s", idxPostResp.StatusCode, strings.TrimSpace(string(idxRespBody)))}
				}
			}
		}
	}

	// Step 5: Register download client
	dcListURL := fmt.Sprintf("%s/api/v3/downloadclient", baseURL)
	dcListReq, _ := http.NewRequestWithContext(r.Context(), "GET", dcListURL, nil)
	dcListReq.Header.Set("X-Api-Key", inst.APIKey)
	vodarrDownloadClientID := -1
	dcListResp, err := client.Do(dcListReq)
	if err != nil {
		results["downloadClient"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		defer dcListResp.Body.Close()
		var dcs []struct {
			ID             int    `json:"id"`
			Name           string `json:"name"`
			Implementation string `json:"implementation"`
			Fields         []struct {
				Name  string      `json:"name"`
				Value interface{} `json:"value"`
			} `json:"fields"`
		}
		dcExists := false
		if json.NewDecoder(dcListResp.Body).Decode(&dcs) == nil {
			for _, dc := range dcs {
				if dc.Implementation != "QBittorrent" {
					continue
				}
				portMatches := false
				for _, f := range dc.Fields {
					if f.Name == "port" {
						if v, ok := f.Value.(float64); ok && int(v) == cfg.Server.QbitPort {
							portMatches = true
						}
					}
				}
				if portMatches {
					dcExists = true
					if vodarrDownloadClientID < 0 || strings.EqualFold(dc.Name, "VODarr") {
						vodarrDownloadClientID = dc.ID
					}
				}
			}
		}
		if dcExists {
			results["downloadClient"] = map[string]interface{}{"success": true, "skipped": "already configured"}
		} else {
			qbitHostname := h.requestHost(r)
			dc := map[string]interface{}{
				"name":               "VODarr",
				"implementation":     "QBittorrent",
				"implementationName": "qBittorrent",
				"configContract":     "QBittorrentSettings",
				"enable":             true,
				"protocol":           "torrent",
				"priority":           1,
				"tags":               []int{},
				"fields": []map[string]interface{}{
					{"name": "host", "value": qbitHostname},
					{"name": "port", "value": cfg.Server.QbitPort},
					{"name": "useSsl", "value": false},
					{"name": "urlBase", "value": ""},
					{"name": "username", "value": ""},
					{"name": "password", "value": ""},
					{"name": "category", "value": "vodarr"},
					{"name": "recentTvPriority", "value": 0},
					{"name": "olderTvPriority", "value": 0},
					{"name": "initialState", "value": 0},
					{"name": "sequentialOrder", "value": false},
					{"name": "firstAndLast", "value": false},
				},
			}
			dcBody, _ := json.Marshal(dc)
			dcPostReq, _ := http.NewRequestWithContext(r.Context(), "POST", dcListURL, bytes.NewReader(dcBody))
			dcPostReq.Header.Set("X-Api-Key", inst.APIKey)
			dcPostReq.Header.Set("Content-Type", "application/json")
			dcPostResp, err := client.Do(dcPostReq)
			if err != nil {
				results["downloadClient"] = map[string]interface{}{"success": false, "error": err.Error()}
			} else {
				dcRespBody, _ := io.ReadAll(dcPostResp.Body)
				dcPostResp.Body.Close()
				if dcPostResp.StatusCode < 300 {
					var created struct {
						ID int `json:"id"`
					}
					if json.Unmarshal(dcRespBody, &created) == nil && created.ID > 0 {
						vodarrDownloadClientID = created.ID
					}
					results["downloadClient"] = map[string]interface{}{"success": true}
				} else {
					results["downloadClient"] = map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d: %s", dcPostResp.StatusCode, strings.TrimSpace(string(dcRespBody)))}
				}
			}
		}
	}

	// Step 6: Bind the VODarr indexer to the VODarr download client.
	if vodarrDownloadClientID > 0 {
		idxBindReq, _ := http.NewRequestWithContext(r.Context(), "GET", indexerListURL, nil)
		idxBindReq.Header.Set("X-Api-Key", inst.APIKey)
		idxBindResp, err := client.Do(idxBindReq)
		if err != nil {
			results["indexerBinding"] = map[string]interface{}{"success": false, "error": err.Error()}
		} else {
			defer idxBindResp.Body.Close()
			var indexers []map[string]interface{}
			if err := json.NewDecoder(idxBindResp.Body).Decode(&indexers); err != nil {
				results["indexerBinding"] = map[string]interface{}{"success": false, "error": "failed to parse indexer list"}
			} else {
				bound := false
				newznabPort := fmt.Sprintf(":%d", cfg.Server.NewznabPort)
				for _, idx := range indexers {
					impl, _ := idx["implementation"].(string)
					if impl != "Newznab" && impl != "Torznab" {
						continue
					}
					fields, _ := idx["fields"].([]interface{})
					isVodarrIndexer := false
					for _, raw := range fields {
						field, _ := raw.(map[string]interface{})
						if field == nil {
							continue
						}
						if field["name"] == "baseUrl" {
							if u, ok := field["value"].(string); ok && strings.Contains(u, newznabPort) {
								isVodarrIndexer = true
								break
							}
						}
					}
					if !isVodarrIndexer {
						continue
					}

					idx["downloadClientId"] = vodarrDownloadClientID
					idx["tags"] = []int{}

					idFloat, ok := idx["id"].(float64)
					if !ok || idFloat <= 0 {
						continue
					}
					updateURL := fmt.Sprintf("%s/api/v3/indexer/%d", baseURL, int(idFloat))
					body, _ := json.Marshal(idx)
					upResp, err := doWithRetry(http.MethodPut, updateURL, body)
					if err != nil {
						results["indexerBinding"] = map[string]interface{}{"success": false, "error": err.Error()}
						break
					}
					upRespBody, _ := io.ReadAll(upResp.Body)
					upResp.Body.Close()
					if upResp.StatusCode >= 300 {
						results["indexerBinding"] = map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d: %s", upResp.StatusCode, strings.TrimSpace(string(upRespBody)))}
						break
					}
					bound = true
				}
				if bound {
					results["indexerBinding"] = map[string]interface{}{"success": true}
				} else if _, exists := results["indexerBinding"]; !exists {
					results["indexerBinding"] = map[string]interface{}{"success": false, "error": "VODarr indexer not found"}
				}
			}
		}
	} else {
		results["indexerBinding"] = map[string]interface{}{"success": false, "error": "VODarr download client not found"}
	}

	h.writeJSON(w, results)
}

// ensureTag returns the ID of the "vodarr" tag in the given arr instance,
// creating it if it does not exist. Returns -1 on error.
func (h *Handler) ensureTag(ctx context.Context, client *http.Client, baseURL, apiKey, label string) int {
	tagURL := fmt.Sprintf("%s/api/v3/tag", baseURL)
	req, _ := http.NewRequestWithContext(ctx, "GET", tagURL, nil)
	req.Header.Set("X-Api-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()

	var tags []struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return -1
	}
	for _, t := range tags {
		if t.Label == label {
			return t.ID
		}
	}

	// Create the tag
	body, _ := json.Marshal(map[string]string{"label": label})
	postReq, _ := http.NewRequestWithContext(ctx, "POST", tagURL, bytes.NewReader(body))
	postReq.Header.Set("X-Api-Key", apiKey)
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := client.Do(postReq)
	if err != nil {
		return -1
	}
	defer postResp.Body.Close()
	var created struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&created); err != nil {
		return -1
	}
	return created.ID
}

// webhookURL returns the base URL that arr should POST to.
// Always derived from the incoming request's Host header (the web server port).
func (h *Handler) webhookURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// newznabBaseURL returns the base URL for the VODarr Newznab indexer,
// using the hostname from the request and the configured newznab port.
func (h *Handler) newznabBaseURL(r *http.Request, port int) string {
	hostname := r.Host
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, hostname, port)
}

// requestHost returns just the hostname (no port) from the request's Host header.
func (h *Handler) requestHost(r *http.Request) string {
	hostname := r.Host
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		return host
	}
	return hostname
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

func (h *Handler) handleStrmRefresh(w http.ResponseWriter, r *http.Request) {
	if h.writer == nil {
		http.Error(w, "strm writer not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.refreshing.CompareAndSwap(false, true) {
		http.Error(w, "refresh already in progress", http.StatusServiceUnavailable)
		return
	}
	defer h.refreshing.Store(false)

	h.cfgMu.RLock()
	xc := xtream.NewClient(h.cfg.Xtream.URL, h.cfg.Xtream.Username, h.cfg.Xtream.Password)
	h.cfgMu.RUnlock()

	buildURL := func(streamType, streamIDStr, ext string) (string, error) {
		id, err := strconv.Atoi(streamIDStr)
		if err != nil {
			return "", fmt.Errorf("invalid stream id %q: %w", streamIDStr, err)
		}
		url := xc.BuildStreamURL(streamType, id, ext)
		if url == "" {
			return "", fmt.Errorf("unknown stream type %q", streamType)
		}
		return url, nil
	}

	n, err := h.writer.RefreshURLs(buildURL)
	if err != nil {
		slog.Warn("strm refresh completed with errors", "rewritten", n, "error", err)
	} else {
		slog.Info("strm refresh complete", "rewritten", n)
	}

	resp := map[string]interface{}{"rewritten": n}
	if err != nil {
		resp["error"] = err.Error()
	}
	h.writeJSON(w, resp)
}

func (h *Handler) handleSyncHistory(w http.ResponseWriter, r *http.Request) {
	history := h.scheduler.SyncHistory()
	if history == nil {
		history = []vodarrsync.SyncRun{}
	}

	total := len(history)

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			if v > 100 {
				v = 100
			}
			limit = v
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	if offset >= total {
		history = []vodarrsync.SyncRun{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		history = history[offset:end]
	}

	h.writeJSON(w, map[string]interface{}{"total": total, "runs": history})
}
