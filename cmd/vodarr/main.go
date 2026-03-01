package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/newznab"
	"github.com/vodarr/vodarr/internal/qbit"
	"github.com/vodarr/vodarr/internal/strm"
	vodarrsync "github.com/vodarr/vodarr/internal/sync"
	"github.com/vodarr/vodarr/internal/tmdb"
	"github.com/vodarr/vodarr/internal/web"
	"github.com/vodarr/vodarr/internal/xtream"
)

func main() {
	configPath := flag.String("config", "config.yml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	slog.Info("vodarr starting",
		"newznab_port", cfg.Server.NewznabPort,
		"qbit_port", cfg.Server.QbitPort,
		"web_port", cfg.Server.WebPort,
	)

	// Build components
	xc := xtream.NewClient(cfg.Xtream.URL, cfg.Xtream.Username, cfg.Xtream.Password)
	tc := tmdb.NewClient(cfg.TMDB.APIKey)
	idx := index.New()
	strmWriter := strm.NewWriter(cfg.Output.Path, cfg.Output.MoviesDir, cfg.Output.SeriesDir)
	qbitStore := qbit.NewStore()

	scheduler := vodarrsync.NewScheduler(cfg, xc, tc, idx)

	// 1B: Use configured external URL; fall back to request Host header (handled in newznab handler)
	newznabSrvURL := cfg.Server.ExternalURL
	if newznabSrvURL == "" {
		newznabSrvURL = fmt.Sprintf("http://localhost:%d", cfg.Server.NewznabPort)
	}

	// 2C: Pass APIKey from config
	newznabHandler := newznab.NewHandler(idx, cfg.Server.APIKey, newznabSrvURL)
	// 2D: Pass qBit credentials from config
	qbitHandler := qbit.NewHandler(qbitStore, strmWriter, xc, cfg.Output.Path, cfg.Server.QbitUsername, cfg.Server.QbitPassword)
	// 2E: Pass web credentials from config
	webHandler := web.NewHandler(idx, scheduler, web.StaticFS(), cfg.Server.WebUsername, cfg.Server.WebPassword)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start sync scheduler
	scheduler.Start(ctx)

	// 3E: Store servers for graceful shutdown
	newznabSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.NewznabPort),
		Handler: newznabMux(newznabHandler),
	}
	qbitSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.QbitPort),
		Handler: qbitHandler,
	}
	webSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.WebPort),
		Handler: webHandler,
	}

	errc := make(chan error, 3)

	go func() {
		slog.Info("newznab API listening", "addr", newznabSrv.Addr)
		errc <- newznabSrv.ListenAndServe()
	}()

	go func() {
		slog.Info("qbit API listening", "addr", qbitSrv.Addr)
		errc <- qbitSrv.ListenAndServe()
	}()

	go func() {
		slog.Info("web API listening", "addr", webSrv.Addr)
		errc <- webSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		scheduler.Stop()
		tc.Stop() // 5B: release TMDB rate limiter ticker

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = newznabSrv.Shutdown(shutdownCtx)
		_ = qbitSrv.Shutdown(shutdownCtx)
		_ = webSrv.Shutdown(shutdownCtx)
	}
}

func newznabMux(h *newznab.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api", h)
	mux.Handle("/api/", h)
	return mux
}
