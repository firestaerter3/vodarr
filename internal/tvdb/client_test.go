package tvdb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func makeJWT(exp int64) string {
	payload, _ := json.Marshal(map[string]int64{"exp": exp})
	return fmt.Sprintf("header.%s.sig", base64.RawURLEncoding.EncodeToString(payload))
}

func TestJWTExpiryFutureToken(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	tok := makeJWT(future)
	got := jwtExpiry(tok)
	if got.IsZero() {
		t.Fatal("jwtExpiry returned zero time for valid token")
	}
	if got.Unix() != future {
		t.Errorf("exp = %d, want %d", got.Unix(), future)
	}
}

func TestJWTExpiryMalformed(t *testing.T) {
	got := jwtExpiry("notajwt")
	if !got.IsZero() {
		t.Errorf("expected zero time for malformed token, got %v", got)
	}
}

func TestJWTExpiryNoExpClaim(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"sub": "user"})
	tok := fmt.Sprintf("header.%s.sig", base64.RawURLEncoding.EncodeToString(payload))
	got := jwtExpiry(tok)
	if !got.IsZero() {
		t.Errorf("expected zero time for token without exp, got %v", got)
	}
}

// newTestClient wires a Client to the given test server.
// It pre-populates the token so individual tests don't need to serve /login.
func newTestClient(srv *httptest.Server) *Client {
	c := NewClient("testkey")
	c.baseURL = srv.URL
	c.token = "test-token" // skip login in search tests
	return c
}

func TestLoginSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"token":"tok123"}}`))
	}))
	defer srv.Close()

	c := NewClient("testkey")
	c.baseURL = srv.URL

	tok, err := c.EnsureToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok != "tok123" {
		t.Errorf("token = %q, want %q", tok, "tok123")
	}
}

func TestLoginInvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient("badkey")
	c.baseURL = srv.URL

	_, err := c.EnsureToken(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
}

func TestSearchSeriesFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("query") == "" {
			http.Error(w, "missing query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":[{"tvdb_id":"81189","name":"Breaking Bad"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchSeries(context.Background(), "Breaking Bad")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.TVDBID != 81189 {
		t.Errorf("TVDBID = %d, want 81189", result.TVDBID)
	}
	if result.Name != "Breaking Bad" {
		t.Errorf("Name = %q, want Breaking Bad", result.Name)
	}
}

func TestSearchSeriesEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.SearchSeries(context.Background(), "No Such Show")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty search, got %+v", result)
	}
}

func TestSearchSeriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.SearchSeries(context.Background(), "Anything")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestLoginCalledOnlyOnce(t *testing.T) {
	var loginCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			loginCalls++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"token":"tok"}}`))
			return
		}
		// search
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":[{"tvdb_id":"1","name":"Show"}]}`))
	}))
	defer srv.Close()

	c := NewClient("testkey")
	c.baseURL = srv.URL

	c.SearchSeries(context.Background(), "Show A")
	c.SearchSeries(context.Background(), "Show B")

	if loginCalls != 1 {
		t.Errorf("login called %d times, want 1", loginCalls)
	}
}
