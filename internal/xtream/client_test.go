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

func TestBuildStreamURL(t *testing.T) {
	c := NewClient("http://server:8080", "user", "pass")

	cases := []struct {
		streamType string
		id         int
		ext        string
		want       string
	}{
		{"movie", 42, "mkv", "http://server:8080/movie/user/pass/42.mkv"},
		{"series", 99, "ts", "http://server:8080/series/user/pass/99.ts"},
		{"movie", 1, "", "http://server:8080/movie/user/pass/1.mkv"},   // default ext
		{"series", 1, "", "http://server:8080/series/user/pass/1.mkv"}, // default ext
		{"unknown", 1, "mkv", ""},
	}
	for _, tc := range cases {
		got := c.BuildStreamURL(tc.streamType, tc.id, tc.ext)
		if got != tc.want {
			t.Errorf("BuildStreamURL(%q, %d, %q) = %q, want %q", tc.streamType, tc.id, tc.ext, got, tc.want)
		}
	}
}

// TestFlexStringSliceUnmarshal covers the provider inconsistencies that
// previously aborted the whole series sync: backdrop_path arriving as a bare
// string, false, null, "", or the expected array.
func TestFlexStringSliceUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"array", `["a","b"]`, []string{"a", "b"}},
		{"empty array", `[]`, []string{}},
		{"bare string", `"http://img/x.jpg"`, []string{"http://img/x.jpg"}},
		{"empty string", `""`, nil},
		{"false", `false`, nil},
		{"null", `null`, nil},
		{"malformed array", `[1,2]`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s struct {
				B FlexStringSlice `json:"backdrop_path"`
			}
			if err := json.Unmarshal([]byte(`{"backdrop_path":`+tc.in+`}`), &s); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if len(s.B) != len(tc.want) {
				t.Fatalf("in=%s got %v (len %d), want %v (len %d)", tc.in, s.B, len(s.B), tc.want, len(tc.want))
			}
			for i := range tc.want {
				if s.B[i] != tc.want[i] {
					t.Errorf("in=%s index %d = %q, want %q", tc.in, i, s.B[i], tc.want[i])
				}
			}
		})
	}
}

// TestFlexYearUnmarshal covers the provider inconsistency that aborted every
// VOD sync: year arriving as a bare JSON number rather than a string. Roughly
// 10% of the catalogue (1,477 of 14,770 entries when measured) sends a number.
//
// Everything that is not a four-digit year must come out empty. A non-empty
// junk year is worse than no year, because the scheduler only falls back to
// extractNameYear when the field is empty.
func TestFlexYearUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"string", `"2016"`, "2016"},
		{"bare number", `2016`, "2016"},
		{"string with surrounding spaces", `" 2016 "`, "2016"},
		{"empty string", `""`, ""},
		{"false", `false`, ""},
		{"null", `null`, ""},
		{"non-numeric string", `"N/A"`, ""},
		{"fractional string", `"2016.5"`, ""},
		{"fractional number", `2016.5`, ""},
		{"float that renders as integral", `2016.0`, ""},
		{"exponent notation", `1e3`, ""},
		{"float precision trap", `2016.0000000000000001`, ""},
		{"too few digits", `999`, ""},
		{"too many digits", `20161`, ""},
		{"true", `true`, ""},
		{"object", `{"a":1}`, ""},
		{"array", `["2016"]`, ""},
		{"out of float range", `1e400`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s struct {
				Y FlexYear `json:"year"`
			}
			if err := json.Unmarshal([]byte(`{"year":`+tc.in+`}`), &s); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if s.Y.String() != tc.want {
				t.Errorf("in=%s got %q, want %q", tc.in, s.Y.String(), tc.want)
			}
		})
	}
}

// TestVODStreamMixedYearTypes is the regression test for the reported failure:
// a single entry with a numeric year used to fail the whole decode, so no
// stream synced at all. Assert the entire slice survives, not just that the one
// offending field parses.
func TestVODStreamMixedYearTypes(t *testing.T) {
	payload := `[
		{"stream_id":1,"name":"String Year","year":"1999"},
		{"stream_id":2,"name":"Numeric Year","year":2016},
		{"stream_id":3,"name":"No Year","year":""},
		{"stream_id":4,"name":"Null Year","year":null}
	]`
	var streams []VODStream
	if err := json.Unmarshal([]byte(payload), &streams); err != nil {
		t.Fatalf("one non-conforming year aborted the whole decode: %v", err)
	}
	if len(streams) != 4 {
		t.Fatalf("got %d streams, want 4", len(streams))
	}
	want := []string{"1999", "2016", "", ""}
	for i, w := range want {
		if got := streams[i].Year.String(); got != w {
			t.Errorf("streams[%d].Year = %q, want %q", i, got, w)
		}
	}
}
