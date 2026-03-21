package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Manager handles HTTP downloads of media files with concurrency limiting,
// retry/backoff, stall detection, and HTTP Range resume support.
type Manager struct {
	sem            chan struct{}
	client         *http.Client
	maxRetries     int
	retryDelay     time.Duration
	stallTimeout   time.Duration
	pauseDur       time.Duration // how long to auto-pause on provider errors
	interDelay     time.Duration // delay between consecutive downloads
	bandwidthLimit int64         // bytes/sec; 0 = unlimited

	// Provider-error auto-pause: if the provider returns 403/429, we pause
	// all new downloads for this duration to avoid cascading failures.
	pauseMu    sync.RWMutex
	pauseUntil time.Time
}

// Options configures the download manager.
type Options struct {
	MaxConcurrent  int
	InterDelay     time.Duration // pause between consecutive downloads (default 30s)
	BandwidthLimit int64         // bytes/sec; 0 = unlimited
}

func NewManager(opts Options) *Manager {
	if opts.MaxConcurrent < 1 {
		opts.MaxConcurrent = 1
	}
	if opts.InterDelay == 0 {
		opts.InterDelay = 30 * time.Second
	}
	return &Manager{
		sem: make(chan struct{}, opts.MaxConcurrent),
		client: &http.Client{
			Timeout: 0, // no overall timeout; we use stall detection instead
			Transport: &http.Transport{
				// Disable compression so bandwidth limiting is accurate on the wire
				DisableCompression: true,
			},
		},
		maxRetries:     3,
		retryDelay:     5 * time.Second,
		stallTimeout:   30 * time.Second,
		pauseDur:       60 * time.Second,
		interDelay:     opts.InterDelay,
		bandwidthLimit: opts.BandwidthLimit,
	}
}

// ProgressFunc is called periodically during download with current bytes
// downloaded and total bytes (0 if unknown).
type ProgressFunc func(downloaded, total int64)

// Download fetches url to destPath with progress reporting. It blocks until a
// concurrency slot is available. The download is written to a .part temp file
// and atomically renamed on completion. Supports HTTP Range resume on retry.
func (m *Manager) Download(ctx context.Context, url, destPath string, onProgress ProgressFunc) error {
	// Acquire semaphore slot (or bail on ctx cancel)
	select {
	case m.sem <- struct{}{}:
		defer func() {
			// Inter-download delay: pause before releasing the slot so the next
			// download doesn't start immediately. Makes traffic look like a human
			// watching content rather than an automated pipeline.
			if m.interDelay > 0 {
				slog.Debug("download manager: inter-download cooldown", "delay", m.interDelay)
				select {
				case <-time.After(m.interDelay):
				case <-ctx.Done():
				}
			}
			<-m.sem
		}()
	case <-ctx.Done():
		return ctx.Err()
	}

	partPath := destPath + ".part"

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		if attempt > 0 {
			delay := m.retryDelay * (1 << (attempt - 1)) // exponential backoff
			slog.Warn("download retry", "attempt", attempt, "delay", delay, "url", url, "error", lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Respect auto-pause from provider errors
		if err := m.waitForPause(ctx); err != nil {
			return err
		}

		lastErr = m.doAttempt(ctx, url, partPath, onProgress)
		if lastErr == nil {
			// Atomic rename .part -> final
			if err := os.Rename(partPath, destPath); err != nil {
				return fmt.Errorf("rename .part to final: %w", err)
			}
			return nil
		}

		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}

		// Check if this is a provider block (403/429) -- trigger auto-pause
		var httpErr *HTTPError
		if errors.As(lastErr, &httpErr) && (httpErr.StatusCode == 403 || httpErr.StatusCode == 429) {
			m.triggerPause()
		}
	}

	return fmt.Errorf("download failed after %d attempts: %w", m.maxRetries+1, lastErr)
}

func (m *Manager) doAttempt(ctx context.Context, url, partPath string, onProgress ProgressFunc) error {
	// Check how many bytes we already have for potential resume
	var offset int64
	if fi, err := os.Stat(partPath); err == nil {
		offset = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "VLC/3.0.21 LibVLC/3.0.21")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Full response — start from scratch
		offset = 0
	case http.StatusPartialContent:
		// Resume accepted
	case http.StatusRequestedRangeNotSatisfiable:
		// File might be complete already or offset is wrong; start over
		offset = 0
		resp.Body.Close()
		return m.doAttempt(ctx, url, partPath, onProgress) // recurse once without Range
	case http.StatusForbidden, http.StatusTooManyRequests,
		http.StatusServiceUnavailable, http.StatusBadGateway:
		return &HTTPError{StatusCode: resp.StatusCode}
	default:
		if resp.StatusCode >= 400 {
			return &HTTPError{StatusCode: resp.StatusCode}
		}
	}

	var totalSize int64
	if resp.ContentLength > 0 {
		totalSize = offset + resp.ContentLength
	}

	// Open .part file for writing (append if resuming, create if fresh)
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}

	f, err := os.OpenFile(partPath, flags, 0644)
	if err != nil {
		return fmt.Errorf("open part file: %w", err)
	}
	defer f.Close()

	downloaded := offset

	// Layer readers: bandwidth throttle (if configured) → stall detection
	var bodyReader io.Reader = resp.Body
	if m.bandwidthLimit > 0 {
		bodyReader = newThrottledReader(bodyReader, m.bandwidthLimit)
	}
	sr := newStallReader(ctx, bodyReader, m.stallTimeout)

	buf := make([]byte, 256*1024) // 256 KB chunks
	for {
		n, readErr := sr.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return fmt.Errorf("write to part file: %w", wErr)
			}
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, totalSize)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("read body: %w", readErr)
		}
	}

	return nil
}

func (m *Manager) triggerPause() {
	m.pauseMu.Lock()
	defer m.pauseMu.Unlock()
	until := time.Now().Add(m.pauseDur)
	if until.After(m.pauseUntil) {
		m.pauseUntil = until
		slog.Warn("download manager: auto-pausing due to provider error (403/429)", "duration", m.pauseDur)
	}
}

func (m *Manager) waitForPause(ctx context.Context) error {
	m.pauseMu.RLock()
	until := m.pauseUntil
	m.pauseMu.RUnlock()

	remaining := time.Until(until)
	if remaining <= 0 {
		return nil
	}

	slog.Info("download manager: waiting for provider cooldown", "remaining", remaining.Round(time.Second))
	select {
	case <-time.After(remaining):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HTTPError represents an HTTP response with a non-success status code.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("http status %d", e.StatusCode)
}

// throttledReader limits read throughput to a target bytes/sec rate using a
// token-bucket approach. This makes download traffic look like normal streaming
// playback rather than a max-speed bulk transfer.
type throttledReader struct {
	r         io.Reader
	limit     int64 // bytes per second
	lastRead  time.Time
	allowance float64
}

func newThrottledReader(r io.Reader, bytesPerSec int64) *throttledReader {
	return &throttledReader{
		r:         r,
		limit:     bytesPerSec,
		lastRead:  time.Now(),
		allowance: float64(bytesPerSec),
	}
}

func (t *throttledReader) Read(p []byte) (int, error) {
	now := time.Now()
	elapsed := now.Sub(t.lastRead).Seconds()
	t.lastRead = now
	t.allowance += elapsed * float64(t.limit)
	if t.allowance > float64(t.limit) {
		t.allowance = float64(t.limit)
	}

	if t.allowance < 1 {
		sleepFor := time.Duration((1 - t.allowance) / float64(t.limit) * float64(time.Second))
		time.Sleep(sleepFor)
		t.allowance = 1
		t.lastRead = time.Now()
	}

	// Cap read size to available allowance
	maxRead := int(t.allowance)
	if maxRead > len(p) {
		maxRead = len(p)
	}

	n, err := t.r.Read(p[:maxRead])
	t.allowance -= float64(n)
	return n, err
}

// stallReader wraps an io.Reader and returns an error if no data arrives
// within the configured timeout or the context is cancelled.
type stallReader struct {
	ctx     context.Context
	r       io.Reader
	timeout time.Duration
}

func newStallReader(ctx context.Context, r io.Reader, timeout time.Duration) *stallReader {
	return &stallReader{ctx: ctx, r: r, timeout: timeout}
}

func (s *stallReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := s.r.Read(p)
		ch <- result{n, err}
	}()
	select {
	case res := <-ch:
		return res.n, res.err
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	case <-time.After(s.timeout):
		return 0, fmt.Errorf("stall detected: no data for %s", s.timeout)
	}
}
