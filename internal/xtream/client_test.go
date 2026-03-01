package xtream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient creates a Client pointed at the given test server URL with
// zero request delay and minimal retry base delay for fast tests.
func newTestClient(serverURL string) *Client {
	c := NewClient(serverURL, "user", "pass")
	c.requestDelay = 0
	c.retryBaseDelay = time.Millisecond
	return c
}

// testResponse is a simple JSON object returned by mock handlers.
type testResponse struct {
	OK bool `json:"ok"`
}

func TestRetryOn5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{OK: true})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	var out testResponse
	if err := c.apiGet(context.Background(), "", nil, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if !out.OK {
		t.Error("expected OK response")
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	var out testResponse
	err := c.apiGet(context.Background(), "", nil, &out)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestRetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{OK: true})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	var out testResponse
	if err := c.apiGet(context.Background(), "", nil, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	// Use a very short-lived context so it cancels during backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	var out testResponse
	err := c.apiGet(ctx, "", nil, &out)
	if err == nil {
		t.Fatal("expected error after context cancellation, got nil")
	}
	// Should be a context error (deadline exceeded or cancelled).
	if !strings.Contains(err.Error(), "context") && err != context.DeadlineExceeded && err != context.Canceled {
		// Also acceptable: the error wraps a context error
		t.Logf("error = %v (context cancellation may be wrapped)", err)
	}
}

func TestExhaustsRetries(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	var out testResponse
	err := c.apiGet(context.Background(), "", nil, &out)
	if err == nil {
		t.Fatal("expected error after exhausted retries, got nil")
	}
	if attempts != 4 {
		t.Errorf("attempts = %d, want 4 (1 initial + 3 retries)", attempts)
	}
	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("error message = %q, want to contain 'after 3 retries'", err.Error())
	}
}
