package sync

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

// probeSizes sends HTTP HEAD requests to stream URLs to discover file sizes.
// Items whose FileSize is already non-zero in cachedByKey are skipped (smart-skip).
// On failure, FileSize is left at 0 and the Torznab response falls back to 1 GB.
func (s *Scheduler) probeSizes(ctx context.Context, items []*index.Item, cachedByKey map[string]*index.Item) {
	// Build a per-episode FileSize lookup from the cache so we can skip
	// episodes that were already probed in a previous sync.
	cachedEpSize := make(map[int]int64)
	if cachedByKey != nil {
		for _, ci := range cachedByKey {
			for _, ep := range ci.Episodes {
				if ep.FileSize > 0 {
					cachedEpSize[ep.EpisodeID] = ep.FileSize
				}
			}
		}
	}

	type probeTarget struct {
		item *index.Item
		ep   *index.EpisodeItem // nil for movies
		url  string
	}

	var work []probeTarget

	for _, item := range items {
		switch item.Type {
		case index.TypeMovie:
			key := fmt.Sprintf("%s:%d", item.Type, item.XtreamID)
			if cachedByKey != nil {
				if ci, ok := cachedByKey[key]; ok && ci.FileSize > 0 {
					item.FileSize = ci.FileSize
					continue
				}
			}
			work = append(work, probeTarget{
				item: item,
				url:  s.xtream.StreamURL(item.XtreamID, item.ContainerExt),
			})
		case index.TypeSeries:
			for i := range item.Episodes {
				ep := &item.Episodes[i]
				if fs, ok := cachedEpSize[ep.EpisodeID]; ok && fs > 0 {
					ep.FileSize = fs
					continue
				}
				work = append(work, probeTarget{
					item: item,
					ep:   ep,
					url:  s.xtream.SeriesStreamURL(ep.EpisodeID, ep.Ext),
				})
			}
		}
	}

	if len(work) == 0 {
		slog.Info("probe sizes: all items already cached, skipping")
		return
	}

	slog.Info("probing file sizes", "count", len(work))
	s.setProgress("Probing file sizes", 0, len(work))

	client := &http.Client{Timeout: 5 * time.Second}

	parallelism := s.cfg.Sync.Parallelism
	if len(work) < parallelism {
		parallelism = len(work)
	}
	if parallelism < 1 {
		parallelism = 1
	}

	workCh := make(chan probeTarget, len(work))
	for _, w := range work {
		workCh <- w
	}
	close(workCh)

	var progressN int64
	var wg gosync.WaitGroup

	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range workCh {
				if ctx.Err() != nil {
					return
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.url, nil)
				if err == nil {
					resp, err := client.Do(req)
					if err == nil {
						resp.Body.Close()
						if cl := resp.ContentLength; cl > 0 {
							if target.ep != nil {
								target.ep.FileSize = cl
							} else {
								target.item.FileSize = cl
							}
						}
					}
				}
				n := atomic.AddInt64(&progressN, 1)
				s.setProgress("Probing file sizes", int(n), len(work))
			}
		}()
	}
	wg.Wait()
}
