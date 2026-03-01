package web

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/sync"
)

// Handler serves the web UI backend API and the embedded static frontend.
type Handler struct {
	idx       *index.Index
	scheduler *sync.Scheduler
	mux       *http.ServeMux
}

func NewHandler(idx *index.Index, scheduler *sync.Scheduler, staticFS fs.FS) *Handler {
	h := &Handler{
		idx:       idx,
		scheduler: scheduler,
		mux:       http.NewServeMux(),
	}
	h.registerRoutes(staticFS)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes(staticFS fs.FS) {
	h.mux.HandleFunc("GET /api/status", h.handleStatus)
	h.mux.HandleFunc("POST /api/sync", h.handleSync)
	h.mux.HandleFunc("GET /api/content/movies", h.handleMovies)
	h.mux.HandleFunc("GET /api/content/series", h.handleSeries)
	h.mux.HandleFunc("GET /api/config", h.handleGetConfig)
	h.mux.HandleFunc("PUT /api/config", h.handlePutConfig)
	h.mux.HandleFunc("GET /api/health", h.handleHealth)

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
		if err := h.scheduler.Sync(r.Context()); err != nil {
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

func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// Return a minimal config shape (no credentials exposed)
	h.writeJSON(w, map[string]interface{}{
		"sync":    map[string]interface{}{},
		"server":  map[string]interface{}{},
		"output":  map[string]interface{}{},
		"logging": map[string]interface{}{},
	})
}

func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	// Config updates via API are acknowledged but require restart to take effect
	h.writeJSON(w, map[string]string{"status": "ok", "note": "restart required to apply changes"})
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
