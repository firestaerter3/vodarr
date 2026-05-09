package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func makeServer(t *testing.T, releases []githubRelease) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(releases) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCheckStableChannel(t *testing.T) {
	srv, _ := makeServer(t, []githubRelease{
		{TagName: "v1.3.0", Prerelease: true, Draft: false},
		{TagName: "v1.2.0", Prerelease: false, Draft: false},
	})
	c := newWithURL(srv.URL)
	r := c.Check(context.Background(), "1.1.0", false)

	if r.LatestVersion != "1.2.0" {
		t.Errorf("LatestVersion = %q, want 1.2.0", r.LatestVersion)
	}
	if r.ImageTag != imageBase+":"+stableTag {
		t.Errorf("ImageTag = %q, want %q", r.ImageTag, imageBase+":"+stableTag)
	}
	if !r.UpdateAvailable {
		t.Error("UpdateAvailable should be true")
	}
	if r.IsPrerelease {
		t.Error("IsPrerelease should be false for stable pick")
	}
}

func TestCheckBetaChannel(t *testing.T) {
	srv, _ := makeServer(t, []githubRelease{
		{TagName: "v1.3.0-beta.1", Prerelease: true, Draft: false},
		{TagName: "v1.2.0", Prerelease: false, Draft: false},
	})
	c := newWithURL(srv.URL)
	r := c.Check(context.Background(), "1.1.0", true)

	if r.LatestVersion != "1.3.0-beta.1" {
		t.Errorf("LatestVersion = %q, want 1.3.0-beta.1", r.LatestVersion)
	}
	if r.ImageTag != imageBase+":"+betaTag {
		t.Errorf("ImageTag = %q, want %q", r.ImageTag, imageBase+":"+betaTag)
	}
	if !r.IsPrerelease {
		t.Error("IsPrerelease should be true")
	}
}

func TestCheckSkipsDrafts(t *testing.T) {
	srv, _ := makeServer(t, []githubRelease{
		{TagName: "v2.0.0", Prerelease: false, Draft: true},
	})
	c := newWithURL(srv.URL)
	r := c.Check(context.Background(), "1.0.0", false)

	if r.LatestVersion != "" {
		t.Errorf("LatestVersion = %q, want empty (all drafts)", r.LatestVersion)
	}
	if r.UpdateAvailable {
		t.Error("UpdateAvailable should be false when no usable release found")
	}
}

func TestCacheTTL(t *testing.T) {
	srv, calls := makeServer(t, []githubRelease{
		{TagName: "v1.2.0", Prerelease: false, Draft: false},
	})
	c := newWithURL(srv.URL)
	c.Check(context.Background(), "1.1.0", false)
	c.Check(context.Background(), "1.1.0", false)

	if n := calls.Load(); n != 1 {
		t.Errorf("server called %d times, want 1 (cache should serve second call)", n)
	}
}

func TestChannelSwitchInvalidatesCache(t *testing.T) {
	srv, calls := makeServer(t, []githubRelease{
		{TagName: "v1.3.0-beta.1", Prerelease: true, Draft: false},
		{TagName: "v1.2.0", Prerelease: false, Draft: false},
	})
	c := newWithURL(srv.URL)
	c.Check(context.Background(), "1.1.0", false)
	c.Check(context.Background(), "1.1.0", true) // channel changed → cache miss

	if n := calls.Load(); n != 2 {
		t.Errorf("server called %d times, want 2 (channel switch should invalidate cache)", n)
	}
}

func TestIsNewerDev(t *testing.T) {
	if !isNewer("1.0.0", "dev") {
		t.Error("isNewer(1.0.0, dev) should be true")
	}
	if isNewer("", "dev") {
		t.Error("isNewer('', dev) should be false — no latest known")
	}
}

func TestIsNewerSemver(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.3.0", "1.2.9", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.0", "1.2.3", false},
		{"2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.1", false},
	}
	for _, tc := range cases {
		got := isNewer(tc.latest, tc.current)
		if got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := newWithURL(srv.URL)
	r := c.Check(context.Background(), "1.0.0", false)

	if r.Error == "" {
		t.Error("Error field should be set on non-200 response")
	}
	if r.UpdateAvailable {
		t.Error("UpdateAvailable should be false on error")
	}
}

func TestInvalidateCache(t *testing.T) {
	srv, calls := makeServer(t, []githubRelease{
		{TagName: "v1.2.0", Prerelease: false, Draft: false},
	})
	c := newWithURL(srv.URL)
	c.Check(context.Background(), "1.1.0", false)
	c.InvalidateCache()
	c.Check(context.Background(), "1.1.0", false)

	if n := calls.Load(); n != 2 {
		t.Errorf("server called %d times, want 2 (InvalidateCache should force re-fetch)", n)
	}
}
