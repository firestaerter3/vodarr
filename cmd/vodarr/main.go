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

	newznabSrvURL := fmt.Sprintf("http://localhost:%d", cfg.Server.NewznabPort)
	newznabHandler := newznab.NewHandler(idx, "", newznabSrvURL)
	qbitHandler := qbit.NewHandler(qbitStore, strmWriter, xc, cfg.Output.Path)
	webHandler := web.NewHandler(idx, scheduler, web.StaticFS())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start sync scheduler
	scheduler.Start(ctx)

	// Start servers
	errc := make(chan error, 3)

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.NewznabPort)
		slog.Info("newznab API listening", "addr", addr)
		mux := http.NewServeMux()
		mux.Handle("/api", newznabHandler)
		mux.Handle("/api/", newznabHandler)
		errc <- http.ListenAndServe(addr, mux)
	}()

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.QbitPort)
		slog.Info("qbit API listening", "addr", addr)
		errc <- http.ListenAndServe(addr, qbitHandler)
	}()

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.WebPort)
		slog.Info("web API listening", "addr", addr)
		errc <- http.ListenAndServe(addr, webHandler)
	}()

	select {
	case err := <-errc:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		slog.Info("shutting down")
		scheduler.Stop()
	}
}
