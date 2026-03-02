package sync

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
	"github.com/vodarr/vodarr/internal/tmdb"
	"github.com/vodarr/vodarr/internal/xtream"
)

// iptvPrefixRe matches one or more stacked leading IPTV category prefixes such as
// "| NL |", "| NL | HD |", "┃NL┃", etc. in a single pass.
// It handles both ASCII pipe (|, U+007C) and the Unicode box-drawing vertical
// bar (┃, U+2503) that some providers use, plus any leading whitespace/tabs.
var iptvPrefixRe = regexp.MustCompile(`^[\s]*[|┃]\s*(?:[^|┃]+[|┃]\s*)+`)

// yearInParensRe matches a trailing parenthesised 4-digit year, e.g. "(1993)".
var yearInParensRe = regexp.MustCompile(`\s*\((\d{4})\)\s*$`)

// extractTrailingYear returns the 4-digit year embedded at the end of a name
// like "Vrouwenvleugel (1993)", or "" if none is found.
func extractTrailingYear(name string) string {
	if m := yearInParensRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// cleanTitleForSearch strips IPTV prefixes, trailing parenthesised years, and
// user-defined patterns from a stream name before passing it to TMDB search.
func cleanTitleForSearch(name string, patterns []*regexp.Regexp) string {
	title := iptvPrefixRe.ReplaceAllString(name, "")
	title = yearInParensRe.ReplaceAllString(title, "")
	for _, re := range patterns {
		title = re.ReplaceAllString(title, "")
	}
	return strings.TrimSpace(title)
}

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
	cfg          *config.Config
	xtream       *xtream.Client
	tmdb         *tmdb.Client
	idx          *index.Index
	userPatterns []*regexp.Regexp
	cachePath    string

	mu      gosync.RWMutex // 3A: protects status field
	syncMu  gosync.Mutex   // 3B: serialises concurrent Sync calls
	status  Status
	cancel  context.CancelFunc
}

func NewScheduler(cfg *config.Config, xc *xtream.Client, tc *tmdb.Client, idx *index.Index) *Scheduler {
	var patterns []*regexp.Regexp
	for _, p := range cfg.Sync.TitleCleanupPatterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			slog.Warn("invalid title_cleanup_pattern, skipping", "pattern", p, "error", err)
			continue
		}
		patterns = append(patterns, re)
	}
	return &Scheduler{
		cfg:          cfg,
		xtream:       xc,
		tmdb:         tc,
		idx:          idx,
		userPatterns: patterns,
		cachePath:    CachePath(cfg.Output.Path),
	}
}

// Start begins the sync scheduler. If a cache exists it is loaded immediately
// so the index is populated before the first sync completes.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Populate the index from the persisted cache so Newznab returns results
	// immediately on restart, before the first sync finishes.
	if cached, err := LoadIndexCache(s.cachePath); err != nil {
		slog.Warn("index cache load failed, starting empty", "error", err)
	} else if cached != nil && len(cached.Items) > 0 {
		s.idx.Replace(cached.Items)
		movies, series := s.idx.Counts()
		slog.Info("loaded index from cache", "movies", movies, "series", series, "cached_at", cached.Timestamp)
	}

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

	// Build enrichment skip map from the previous sync's cache. Items whose
	// name and type are unchanged can reuse their IDs without hitting TMDB.
	var cachedByKey map[string]*index.Item
	if cached, err := LoadIndexCache(s.cachePath); err == nil && cached != nil {
		cachedByKey = make(map[string]*index.Item, len(cached.Items))
		for _, ci := range cached.Items {
			cachedByKey[fmt.Sprintf("%s:%d", ci.Type, ci.XtreamID)] = ci
		}
	}

	enriched, err := s.enrich(ctx, items, cachedByKey)
	if err != nil {
		// Enrichment errors are non-fatal; we log and use what we have
		slog.Warn("enrichment completed with errors", "error", err)
	}

	s.idx.Replace(enriched)
	movies, series := s.idx.Counts()
	slog.Info("sync complete", "movies", movies, "series", series)

	if err := SaveIndexCache(s.cachePath, enriched); err != nil {
		slog.Warn("index cache save failed", "error", err)
	}

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

		year := st.Year
		if year == "" {
			year = extractTrailingYear(st.Name)
		}
		item := &index.Item{
			Type:         index.TypeMovie,
			XtreamID:     st.ID.Int(),
			Name:         st.Name,
			Year:         year,
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

	// Build a lastModified lookup from the bulk response.
	lastModifiedByID := make(map[int]string, len(series))
	for _, sr := range series {
		lastModifiedByID[sr.SeriesID.Int()] = sr.LastModified
	}

	// Load previous snapshot for smart-skip.
	snapPath := SnapshotPath(s.cfg.Output.Path)
	prevSnap, err := LoadSnapshot(snapPath)
	if err != nil {
		slog.Warn("snapshot load failed, rebuilding", "error", err)
	}

	// Partition: series we can reconstruct from the snapshot vs. those that need a fetch.
	type seriesWork struct {
		series xtream.Series
	}
	skippedItems := make(map[int]*index.Item, len(series))
	var toFetch []xtream.Series

	for _, sr := range series {
		id := sr.SeriesID.Int()
		if prevSnap != nil {
			if prev, ok := prevSnap.Series[id]; ok {
				cs := SeriesChecksum(sr.Name, sr.LastModified, prev.EpisodeCount)
				if cs == prev.Checksum && len(prev.Episodes) > 0 {
					item := buildSeriesItem(sr)
					item.Episodes = prev.Episodes
					skippedItems[id] = item
					continue
				}
			}
		}
		toFetch = append(toFetch, sr)
	}

	slog.Info("series smart skip", "skipped", len(skippedItems), "fetch", len(toFetch))
	s.setProgress("Fetching series details", 0, len(toFetch))

	// Worker pool for series that need a GetSeriesInfo call.
	parallelism := s.cfg.Sync.Parallelism
	if len(toFetch) < parallelism {
		parallelism = len(toFetch)
	}
	if parallelism < 1 {
		parallelism = 1
	}

	workCh := make(chan seriesWork, len(toFetch))
	for _, sr := range toFetch {
		workCh <- seriesWork{sr}
	}
	close(workCh)

	fetchedItems := make(map[int]*index.Item, len(toFetch))
	var fetchMu gosync.Mutex
	var progressN int64

	var wg gosync.WaitGroup
	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				if ctx.Err() != nil {
					return
				}
				sr := work.series
				item := buildSeriesItem(sr)

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

				n := atomic.AddInt64(&progressN, 1)
				s.setProgress("Fetching series details", int(n), len(toFetch))

				fetchMu.Lock()
				fetchedItems[sr.SeriesID.Int()] = item
				fetchMu.Unlock()
			}
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Merge skipped + fetched in original bulk order.
	for _, sr := range series {
		id := sr.SeriesID.Int()
		if item, ok := skippedItems[id]; ok {
			items = append(items, item)
		} else if item, ok := fetchedItems[id]; ok {
			items = append(items, item)
		}
	}

	// Persist snapshot for the next sync.
	newSnap := &Snapshot{
		Timestamp: time.Now(),
		Movies:    make(map[int]MovieEntry, len(streams)),
		Series:    make(map[int]SeriesEntry, len(series)),
	}
	for _, st := range streams {
		id := st.ID.Int()
		newSnap.Movies[id] = MovieEntry{
			Name:     st.Name,
			Checksum: MovieChecksum(st.Name, st.ContainerExt),
		}
	}
	for _, sr := range series {
		id := sr.SeriesID.Int()
		var eps []index.EpisodeItem
		if item, ok := skippedItems[id]; ok {
			eps = item.Episodes
		} else if item, ok := fetchedItems[id]; ok {
			eps = item.Episodes
		}
		lm := lastModifiedByID[id]
		newSnap.Series[id] = SeriesEntry{
			Name:         sr.Name,
			LastModified: lm,
			EpisodeCount: len(eps),
			Checksum:     SeriesChecksum(sr.Name, lm, len(eps)),
			Episodes:     eps,
		}
	}
	if err := SaveSnapshot(snapPath, newSnap); err != nil {
		slog.Warn("snapshot save failed", "error", err)
	}

	return items, nil
}

// buildSeriesItem constructs a series index.Item from bulk Xtream metadata
// (without episode data, which must be added separately).
func buildSeriesItem(sr xtream.Series) *index.Item {
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
	if len(sr.ReleaseDate) >= 4 {
		item.Year = sr.ReleaseDate[:4]
	} else if y := extractTrailingYear(sr.Name); y != "" {
		item.Year = y
	}
	if sr.TMDBId.Int() > 0 {
		item.TMDBId = strconv.Itoa(sr.TMDBId.Int())
	}
	return item
}

// enrich resolves TMDB → IMDB + TVDB IDs for items that have a TMDB ID.
// It uses a worker pool to overlap HTTP latency while the TMDB client's
// internal ticker naturally enforces the 30 req/s rate limit.
//
// cachedByKey is an optional map (keyed by "type:xtreamID") of previously
// enriched items. Items found in the cache whose name is unchanged skip TMDB.
func (s *Scheduler) enrich(ctx context.Context, items []*index.Item, cachedByKey map[string]*index.Item) ([]*index.Item, error) {
	if s.cfg.TMDB.APIKey == "" {
		slog.Warn("TMDB API key not set; skipping enrichment")
		return items, nil
	}

	total := len(items)

	parallelism := s.cfg.Sync.Parallelism
	if total < parallelism {
		parallelism = total
	}
	if parallelism < 1 {
		parallelism = 1
	}

	workCh := make(chan *index.Item, total)
	for _, item := range items {
		workCh <- item
	}
	close(workCh)

	var (
		progressN int64
		errMu     gosync.Mutex
		lastErr   error
	)

	var wg gosync.WaitGroup
	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				if ctx.Err() != nil {
					return
				}

				// Reuse cached IDs for unchanged items to avoid redundant TMDB calls.
				if cachedByKey != nil {
					key := fmt.Sprintf("%s:%d", item.Type, item.XtreamID)
					if ci, ok := cachedByKey[key]; ok && ci.Name == item.Name {
						if ci.IMDBId != "" || ci.TVDBId != "" {
							item.IMDBId = ci.IMDBId
							item.TVDBId = ci.TVDBId
							item.TMDBId = ci.TMDBId
							n := atomic.AddInt64(&progressN, 1)
							s.setProgress("Enriching via TMDB", int(n), total)
							continue
						}
					}
				}

				if item.TMDBId == "" {
					if err := s.resolveByTitle(ctx, item); err != nil {
						slog.Debug("title resolve failed", "name", item.Name, "error", err)
					}
				}

				if item.TMDBId != "" {
					tmdbID, err := strconv.Atoi(item.TMDBId)
					if err == nil && tmdbID > 0 {
						var extIDs *tmdb.ExternalIDs
						switch item.Type {
						case index.TypeMovie:
							extIDs, err = s.tmdb.GetMovieExternalIDs(ctx, tmdbID)
						case index.TypeSeries:
							extIDs, err = s.tmdb.GetTVExternalIDs(ctx, tmdbID)
						}
						if err != nil {
							errMu.Lock()
							lastErr = err
							errMu.Unlock()
							slog.Debug("external ids lookup failed", "tmdb_id", tmdbID, "error", err)
						} else if extIDs != nil {
							if extIDs.IMDBID != "" {
								item.IMDBId = extIDs.IMDBID
							}
							if extIDs.TVDBID > 0 {
								item.TVDBId = strconv.Itoa(extIDs.TVDBID)
							}
						}
					}
				}

				n := atomic.AddInt64(&progressN, 1)
				s.setProgress("Enriching via TMDB", int(n), total)
			}
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return items, ctx.Err()
	}

	errMu.Lock()
	err := lastErr
	errMu.Unlock()

	return items, err
}

// resolveByTitle searches TMDB by title to find a TMDB ID.
// ctx is passed through so that sync cancellation (e.g. SIGTERM) propagates
// to TMDB search calls and avoids blocking shutdown on large catalogs.
func (s *Scheduler) resolveByTitle(ctx context.Context, item *index.Item) error {
	year := 0
	if item.Year != "" {
		if y, err := strconv.Atoi(item.Year); err == nil {
			year = y
		}
	}

	title := cleanTitleForSearch(item.Name, s.userPatterns)

	switch item.Type {
	case index.TypeMovie:
		result, err := s.tmdb.SearchMovie(ctx, title, year)
		if err != nil {
			return err
		}
		if result != nil {
			item.TMDBId = strconv.Itoa(result.ID)
		}
	case index.TypeSeries:
		result, err := s.tmdb.SearchTV(ctx, title, year)
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
