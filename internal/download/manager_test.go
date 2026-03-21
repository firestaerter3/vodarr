package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadBasic(t *testing.T) {
	content := "hello world, this is a test video file"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write([]byte(content))
	}))
	defer srv.Close()

	m := NewManager(1)
	dest := filepath.Join(t.TempDir(), "movie.mkv")

	var lastDownloaded, lastTotal int64
	err := m.Download(context.Background(), srv.URL, dest, func(downloaded, total int64) {
		lastDownloaded = downloaded
		lastTotal = total
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content mismatch: got %q, want %q", string(data), content)
	}

	if lastDownloaded != int64(len(content)) {
		t.Errorf("final downloaded = %d, want %d", lastDownloaded, len(content))
	}
	if lastTotal != int64(len(content)) {
		t.Errorf("final total = %d, want %d", lastTotal, len(content))
	}

	// .part file should be cleaned up
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("expected .part file to be removed after successful download")
	}
}

func TestDownloadConcurrencyLimit(t *testing.T) {
	var active int64
	var maxActive int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&active, 1)
		for {
			old := atomic.LoadInt64(&maxActive)
			if cur <= old {
				break
			}
			if atomic.CompareAndSwapInt64(&maxActive, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	m := NewManager(2) // limit to 2 concurrent

	done := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func(i int) {
			dest := filepath.Join(t.TempDir(), fmt.Sprintf("file%d.mkv", i))
			done <- m.Download(context.Background(), srv.URL, dest, nil)
		}(i)
	}

	for i := 0; i < 5; i++ {
		if err := <-done; err != nil {
			t.Errorf("download %d failed: %v", i, err)
		}
	}

	observed := atomic.LoadInt64(&maxActive)
	if observed > 2 {
		t.Errorf("max concurrent downloads = %d, want <= 2", observed)
	}
}

func TestDownloadContextCancel(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		// Block until request context is done (simulates slow provider)
		<-r.Context().Done()
	}))
	defer srv.Close()

	m := NewManager(1)
	m.stallTimeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		dest := filepath.Join(t.TempDir(), "movie.mkv")
		done <- m.Download(ctx, srv.URL, dest, nil)
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error after cancel, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not cancel within 3s")
	}
}

func TestDownloadRetryOnServerError(t *testing.T) {
	var attempts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("success"))
	}))
	defer srv.Close()

	m := NewManager(1)
	m.retryDelay = 10 * time.Millisecond // fast retries for test

	dest := filepath.Join(t.TempDir(), "movie.mkv")
	err := m.Download(context.Background(), srv.URL, dest, nil)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "success" {
		t.Errorf("content = %q, want %q", string(data), "success")
	}

	if got := atomic.LoadInt64(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", got)
	}
}

func TestDownloadHTTPResume(t *testing.T) {
	content := "abcdefghij"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			var offset int64
			fmt.Sscanf(rangeHeader, "bytes=%d-", &offset)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", int64(len(content))-offset))
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte(content[offset:]))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write([]byte(content))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "movie.mkv")
	partPath := dest + ".part"

	// Pre-seed a partial .part file
	if err := os.WriteFile(partPath, []byte("abcde"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(1)
	err := m.Download(context.Background(), srv.URL, dest, nil)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestDownloadAutoPauseOn403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	m := NewManager(1)
	m.retryDelay = 10 * time.Millisecond
	m.maxRetries = 1
	m.pauseDur = 200 * time.Millisecond // short pause for test

	dest := filepath.Join(t.TempDir(), "movie.mkv")
	err := m.Download(context.Background(), srv.URL, dest, nil)
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}

	// Verify auto-pause was triggered
	m.pauseMu.RLock()
	paused := m.pauseUntil.After(time.Now())
	m.pauseMu.RUnlock()
	if !paused {
		t.Error("expected auto-pause to be active after 403 response")
	}
}

func TestDownloadAtomicWrite(t *testing.T) {
	// Verify that partial downloads don't leave a final file
	var attempts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := NewManager(1)
	m.retryDelay = 5 * time.Millisecond
	m.maxRetries = 1

	dest := filepath.Join(t.TempDir(), "movie.mkv")
	_ = m.Download(context.Background(), srv.URL, dest, nil)

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("expected final file not to exist after failed download")
	}
}
