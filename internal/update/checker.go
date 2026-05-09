package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cacheTTL  = 6 * time.Hour
	apiURL    = "https://api.github.com/repos/firestaerter3/vodarr/releases"
	imageBase = "ghcr.io/firestaerter3/vodarr"
	stableTag = "latest"
	betaTag   = "downloadbeta"
)

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// Result is the payload returned by GET /api/update.
type Result struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ImageTag        string `json:"image_tag"`
	IsPrerelease    bool   `json:"is_prerelease"`
	BetaChannel     bool   `json:"beta_channel"`
	Error           string `json:"error,omitempty"`
}

// Checker fetches GitHub releases with a 6-hour in-memory cache. Safe for concurrent use.
type Checker struct {
	mu         sync.Mutex
	httpClient *http.Client
	releaseURL string
	cachedAt   time.Time
	cached     *Result
}

// New returns a Checker using the GitHub releases API.
func New() *Checker {
	return &Checker{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		releaseURL: apiURL,
	}
}

// newWithURL returns a Checker with a custom API URL. Used in tests only.
func newWithURL(url string) *Checker {
	return &Checker{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		releaseURL: url,
	}
}

// InvalidateCache discards the cached result so the next Check performs a fresh fetch.
func (c *Checker) InvalidateCache() {
	c.mu.Lock()
	c.cached = nil
	c.cachedAt = time.Time{}
	c.mu.Unlock()
}

// Check returns update info for currentVersion on the given channel. Results are cached
// for 6h. The cache is auto-invalidated when betaChannel differs from the cached value.
func (c *Checker) Check(ctx context.Context, currentVersion string, betaChannel bool) Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != nil && c.cached.BetaChannel != betaChannel {
		c.cached = nil
	}
	if c.cached != nil && time.Since(c.cachedAt) < cacheTTL {
		r := *c.cached
		r.CurrentVersion = currentVersion
		r.UpdateAvailable = isNewer(r.LatestVersion, currentVersion)
		return r
	}

	r := c.fetch(ctx, currentVersion, betaChannel)
	c.cached = &r
	c.cachedAt = time.Now()
	return r
}

func (c *Checker) fetch(ctx context.Context, currentVersion string, betaChannel bool) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.releaseURL, nil)
	if err != nil {
		return Result{CurrentVersion: currentVersion, BetaChannel: betaChannel, Error: err.Error()}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Result{CurrentVersion: currentVersion, BetaChannel: betaChannel, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{
			CurrentVersion: currentVersion, BetaChannel: betaChannel,
			Error: fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode),
		}
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return Result{
			CurrentVersion: currentVersion, BetaChannel: betaChannel,
			Error: fmt.Sprintf("decoding releases: %v", err),
		}
	}

	var latestVersion string
	var isPrerelease bool
	for _, r := range releases {
		if r.Draft {
			continue
		}
		// Beta channel: pick first non-draft (prerelease or stable).
		// Stable channel: pick first non-draft, non-prerelease.
		if betaChannel || !r.Prerelease {
			latestVersion = strings.TrimPrefix(r.TagName, "v")
			isPrerelease = r.Prerelease
			break
		}
	}

	tag := imageBase + ":" + stableTag
	if betaChannel {
		tag = imageBase + ":" + betaTag
	}

	return Result{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: isNewer(latestVersion, currentVersion),
		ImageTag:        tag,
		IsPrerelease:    isPrerelease,
		BetaChannel:     betaChannel,
	}
}

func isNewer(latest, current string) bool {
	if latest == "" {
		return false
	}
	if current == "dev" || current == "" {
		return true
	}
	return semverGT(latest, current)
}

func semverGT(a, b string) bool {
	pa, pb := semverParts(a), semverParts(b)
	for i := range pa {
		if pa[i] > pb[i] {
			return true
		}
		if pa[i] < pb[i] {
			return false
		}
	}
	return false
}

func semverParts(v string) [3]int {
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		s := strings.SplitN(parts[i], "-", 2)[0]
		for _, c := range s {
			if c < '0' || c > '9' {
				break
			}
			out[i] = out[i]*10 + int(c-'0')
		}
	}
	return out
}
