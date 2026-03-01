package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	gosync "sync"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/tmdb"
	"github.com/vodarr/vodarr/internal/xtream"
)

// Status describes the current sync state.
type Status struct {
	Running     bool      `json:"running"`
	LastSync    time.Time `json:"last_sync"`
	NextSync    time.Time `json:"next_sync"`
	TotalMovies int       `json:"total_movies"`
	TotalSeries int       `json:"total_series"`
	Error       string    `json:"error,omitempty"`
	Progress    Progress  `json:"progress"`
}

// Progress tracks current sync progress.
type Progress struct {
	Stage   string `json:"stage"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
}

// Scheduler manages periodic syncing of the Xtream catalog into the index.
type Scheduler struct {
	cfg    *config.Config
	xtream *xtream.Client
	tmdb   *tmdb.Client
	idx    *index.Index

	mu      gosync.RWMutex // 3A: protects status field
	syncMu  gosync.Mutex   // 3B: serialises concurrent Sync calls
	status  Status
	cancel  context.CancelFunc
}

func NewScheduler(cfg *config.Config, xc *xtream.Client, tc *tmdb.Client, idx *index.Index) *Scheduler {
	return &Scheduler{
		cfg:    cfg,
		xtream: xc,
		tmdb:   tc,
		idx:    idx,
	}
}

// Start begins the sync scheduler. If OnStartup is true, it syncs immediately.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if s.cfg.Sync.OnStartup {
		go func() {
			if err := s.Sync(ctx); err != nil {
				slog.Error("startup sync failed", "error", err)
			}
		}()
	}

	go s.loop(ctx)
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Status returns the current sync status (safe for concurrent reads).
func (s *Scheduler) Status() Status {
	movies, series := s.idx.Counts()
	s.mu.RLock()
	st := s.status
	s.mu.RUnlock()
	st.TotalMovies = movies
	st.TotalSeries = series
	return st
}

// Sync performs a full catalog sync: fetch → enrich → replace index.
// If a sync is already in progress, this call returns immediately (3B).
func (s *Scheduler) Sync(ctx context.Context) error {
	// 3B: Try to acquire the sync mutex; skip if already running
	if !s.syncMu.TryLock() {
		slog.Info("sync already in progress, skipping")
		return nil
	}
	defer s.syncMu.Unlock()

	s.setRunning(true, "")
	slog.Info("sync started")

	defer func() {
		s.setRunning(false, "")
		s.mu.Lock()
		s.status.LastSync = time.Now()
		s.status.NextSync = time.Now().Add(s.cfg.Sync.ParsedInterval)
		s.mu.Unlock()
	}()

	items, err := s.fetchAll(ctx)
	if err != nil {
		s.mu.Lock()
		s.status.Error = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("fetch: %w", err)
	}

	enriched, err := s.enrich(ctx, items)
	if err != nil {
		// Enrichment errors are non-fatal; we log and use what we have
		slog.Warn("enrichment completed with errors", "error", err)
	}

	s.idx.Replace(enriched)
	movies, series := s.idx.Counts()
	slog.Info("sync complete", "movies", movies, "series", series)
	return nil
}

func (s *Scheduler) setRunning(running bool, errMsg string) {
	s.mu.Lock()
	s.status.Running = running
	if !running {
		s.status.Error = errMsg
	}
	s.mu.Unlock()
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Sync.ParsedInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				slog.Error("scheduled sync failed", "error", err)
			}
		}
	}
}

// fetchAll retrieves the full VOD + series catalog from Xtream.
func (s *Scheduler) fetchAll(ctx context.Context) ([]*index.Item, error) {
	var items []*index.Item

	// --- VOD ---
	s.setProgress("Fetching VOD catalog", 0, 0)
	streams, err := s.xtream.GetVODStreams(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("get vod streams: %w", err)
	}
	slog.Debug("fetched vod streams", "count", len(streams))

	for i, st := range streams {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.setProgress("Fetching VOD catalog", i+1, len(streams))

		item := &index.Item{
			Type:         index.TypeMovie,
			XtreamID:     st.ID.Int(),
			Name:         st.Name,
			Year:         st.Year,
			Plot:         st.Plot,
			Genre:        st.Genre,
			Rating:       float64(st.Rating),
			Poster:       st.Poster,
			ReleaseDate:  st.ReleaseDate,
			ContainerExt: st.ContainerExt,
		}
		if st.TMDBId.Int() > 0 {
			item.TMDBId = strconv.Itoa(st.TMDBId.Int())
		}
		items = append(items, item)
	}

	// --- Series ---
	s.setProgress("Fetching series catalog", 0, 0)
	series, err := s.xtream.GetSeries(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("get series: %w", err)
	}
	slog.Debug("fetched series", "count", len(series))

	for i, sr := range series {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.setProgress("Fetching series catalog", i+1, len(series))

		item := &index.Item{
			Type:        index.TypeSeries,
			XtreamID:    sr.SeriesID.Int(),
			Name:        sr.Name,
			Plot:        sr.Plot,
			Genre:       sr.Genre,
			Rating:      float64(sr.Rating),
			Poster:      sr.Cover,
			ReleaseDate: sr.ReleaseDate,
		}
		if sr.TMDBId.Int() > 0 {
			item.TMDBId = strconv.Itoa(sr.TMDBId.Int())
		}
		// Fetch episodes
		if info, err := s.xtream.GetSeriesInfo(ctx, sr.SeriesID.Int()); err == nil {
			for seasonStr, eps := range info.Episodes {
				season, _ := strconv.Atoi(seasonStr)
				for _, ep := range eps {
					item.Episodes = append(item.Episodes, index.EpisodeItem{
						EpisodeID:  ep.ID.Int(),
						Season:     season,
						EpisodeNum: ep.EpisodeNum.Int(),
						Title:      ep.Title,
						Ext:        ep.ContainerExt,
					})
				}
			}
			// 5D: Sort episodes by season then episode number for consistent ordering
			sort.Slice(item.Episodes, func(i, j int) bool {
				if item.Episodes[i].Season != item.Episodes[j].Season {
					return item.Episodes[i].Season < item.Episodes[j].Season
				}
				return item.Episodes[i].EpisodeNum < item.Episodes[j].EpisodeNum
			})
		} else {
			// 4D: Log series info fetch failures
			slog.Warn("series info fetch failed", "series_id", sr.SeriesID.Int(), "error", err)
		}
		items = append(items, item)
	}

	return items, nil
}

// enrich resolves TMDB → IMDB + TVDB IDs for items that have a TMDB ID.
func (s *Scheduler) enrich(ctx context.Context, items []*index.Item) ([]*index.Item, error) {
	total := len(items)
	var lastErr error

	for i, item := range items {
		if ctx.Err() != nil {
			return items, ctx.Err()
		}
		s.setProgress("Enriching via TMDB", i+1, total)

		if item.TMDBId == "" {
			// Try searching by title
			if err := s.resolveByTitle(item); err != nil {
				slog.Debug("title resolve failed", "name", item.Name, "error", err)
			}
		}

		if item.TMDBId == "" {
			continue
		}

		tmdbID, err := strconv.Atoi(item.TMDBId)
		if err != nil || tmdbID <= 0 {
			continue
		}

		var extIDs *tmdb.ExternalIDs
		switch item.Type {
		case index.TypeMovie:
			extIDs, err = s.tmdb.GetMovieExternalIDs(ctx, tmdbID)
		case index.TypeSeries:
			extIDs, err = s.tmdb.GetTVExternalIDs(ctx, tmdbID)
		}
		if err != nil {
			lastErr = err
			slog.Debug("external ids lookup failed", "tmdb_id", tmdbID, "error", err)
			continue
		}
		if extIDs == nil {
			continue
		}
		if extIDs.IMDBID != "" {
			item.IMDBId = extIDs.IMDBID
		}
		if extIDs.TVDBID > 0 {
			item.TVDBId = strconv.Itoa(extIDs.TVDBID)
		}
	}

	return items, lastErr
}

// resolveByTitle searches TMDB by title to find a TMDB ID.
func (s *Scheduler) resolveByTitle(item *index.Item) error {
	year := 0
	if item.Year != "" {
		if y, err := strconv.Atoi(item.Year); err == nil {
			year = y
		}
	}

	switch item.Type {
	case index.TypeMovie:
		result, err := s.tmdb.SearchMovie(context.Background(), item.Name, year)
		if err != nil {
			return err
		}
		if result != nil {
			item.TMDBId = strconv.Itoa(result.ID)
		}
	case index.TypeSeries:
		result, err := s.tmdb.SearchTV(context.Background(), item.Name, year)
		if err != nil {
			return err
		}
		if result != nil {
			item.TMDBId = strconv.Itoa(result.ID)
		}
	}
	return nil
}

func (s *Scheduler) setProgress(stage string, current, total int) {
	s.mu.Lock()
	s.status.Progress = Progress{Stage: stage, Current: current, Total: total}
	s.mu.Unlock()
}
