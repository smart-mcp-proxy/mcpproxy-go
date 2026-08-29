package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	netURL "net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/mod/semver"
)

// releaseTagPattern is the shape a release tag may take before it is
// interpolated into a request path.
var releaseTagPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+[\w.\-+]*$`)

const (
	// GitHubRepo is the repository to check for releases
	GitHubRepo = "smart-mcp-proxy/mcpproxy-go"

	// DefaultAPIBase is the public GitHub REST API root.
	DefaultAPIBase = "https://api.github.com"

	// httpTimeout is the timeout for GitHub API requests
	httpTimeout = 10 * time.Second
)

// GitHubClient handles communication with the GitHub Releases API.
type GitHubClient struct {
	logger     *zap.Logger
	httpClient *http.Client
	repo       string
	// apiBase is the REST API root. Overridable so tests can point the client
	// at an httptest server instead of reaching the real GitHub.
	apiBase string

	// mu guards the conditional-request cache below. The checker calls
	// ListReleases from its background goroutine while CheckNow can call it
	// from a request handler.
	mu sync.Mutex
	// listETag / listCache implement If-None-Match for the releases list, so
	// a repeat fetch of an unchanged list costs no rate-limit budget.
	listETag  string
	listCache []GitHubRelease
}

// NewGitHubClient creates a new GitHub API client.
func NewGitHubClient(logger *zap.Logger) *GitHubClient {
	return &GitHubClient{
		logger: logger,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
		repo:    GitHubRepo,
		apiBase: DefaultAPIBase,
	}
}

// SetAPIBase overrides the REST API root (tests, self-hosted mirrors). An
// empty value restores the public GitHub API.
func (c *GitHubClient) SetAPIBase(base string) {
	if base == "" {
		base = DefaultAPIBase
	}
	c.apiBase = strings.TrimSuffix(base, "/")
}

// GetLatestRelease fetches the latest stable release from GitHub.
func (c *GitHubClient) GetLatestRelease() (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.apiBase, c.repo)

	resp, err := c.httpClient.Get(url) // #nosec G107 -- URL is constructed from known repo constant
	if err != nil {
		c.logger.Debug("Failed to fetch latest release", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Debug("GitHub API returned non-200 status",
			zap.Int("status_code", resp.StatusCode),
			zap.String("url", url))
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		c.logger.Debug("Failed to decode release response", zap.Error(err))
		return nil, fmt.Errorf("failed to decode release: %w", err)
	}

	return &release, nil
}

// ListReleases fetches one page of the releases list, newest-created first.
//
// The result is cached against the response ETag: GitHub answers a repeat
// request carrying If-None-Match with 304 and no body, which does not count
// against the rate limit. That matters because `CheckNow` (the tray's "Check
// for Updates", `/api/v1/info?refresh=true`) deliberately bypasses the failure
// backoff and is user-triggerable at will, and because many installs behind
// one office egress IP share a single 60-request hourly bucket.
func (c *GitHubClient) ListReleases(ctx context.Context, perPage int) ([]GitHubRelease, error) {
	if perPage <= 0 || perPage > releasesPerPage {
		perPage = releasesPerPage
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.apiBase, c.repo, perPage)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to build releases request: %w", err)
	}

	c.mu.Lock()
	etag, cached := c.listETag, c.listCache
	c.mu.Unlock()
	if etag != "" && len(cached) > 0 {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Debug("Failed to fetch releases list", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && len(cached) > 0 {
		c.logger.Debug("Releases list unchanged (304), reusing cached page")
		return cached, nil
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Debug("GitHub API returned non-200 status",
			zap.Int("status_code", resp.StatusCode),
			zap.String("url", url))
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		c.logger.Debug("Failed to decode releases list", zap.Error(err))
		return nil, fmt.Errorf("failed to decode releases: %w", err)
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found")
	}

	if tag := resp.Header.Get("ETag"); tag != "" {
		c.mu.Lock()
		c.listETag, c.listCache = tag, releases
		c.mu.Unlock()
	}

	return releases, nil
}

// GetLatestReleaseIncludingPrereleases fetches the latest release including prereleases.
func (c *GitHubClient) GetLatestReleaseIncludingPrereleases() (*GitHubRelease, error) {
	releases, err := c.ListReleases(context.Background(), releasesPerPage)
	if err != nil {
		return nil, err
	}

	// Pick the highest version by semver, not the first entry. GitHub orders
	// this list by created_at, which is neither publish order nor semver
	// order — the live data contains v0.53.0-rc.9 sitting ahead of the
	// later-published v0.53.0-rc.10, so "releases[0]" could offer an RC user
	// a release older than the one they already run.
	var newest *GitHubRelease
	for i := range releases {
		tag := ensureVPrefix(releases[i].TagName)
		if !semver.IsValid(tag) {
			continue
		}
		if newest == nil || semver.Compare(tag, ensureVPrefix(newest.TagName)) > 0 {
			newest = &releases[i]
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("no releases with a valid version found")
	}
	return newest, nil
}

// GetRelease fetches the appropriate release based on whether prereleases should be included.
func (c *GitHubClient) GetRelease(includePrereleases bool) (*GitHubRelease, error) {
	if includePrereleases {
		return c.GetLatestReleaseIncludingPrereleases()
	}
	return c.GetLatestRelease()
}

// GetReleaseByTag fetches one specific release by its git tag. `mcpproxy
// update --version vX.Y.Z` needs it: the latest/list endpoints cannot address
// an older (or a specific prerelease) tag, and FR-022 allows a deliberate
// downgrade only when the user names the exact version.
func (c *GitHubClient) GetReleaseByTag(tag string) (*GitHubRelease, error) {
	return c.GetReleaseByTagContext(context.Background(), tag)
}

// GetReleaseByTagContext is GetReleaseByTag bounded by ctx. The delta scan uses
// it so its budget covers the request itself, not merely the decision to start
// one.
func (c *GitHubClient) GetReleaseByTagContext(ctx context.Context, tag string) (*GitHubRelease, error) {
	if tag == "" {
		return nil, fmt.Errorf("release tag is required")
	}
	// The tag reaches the path unescaped, and the delta scan now feeds this
	// the ldflags-stamped build version rather than a user-typed flag value.
	// Validate the shape here instead of relying on every caller.
	if !releaseTagPattern.MatchString(tag) {
		return nil, fmt.Errorf("invalid release tag %q", tag)
	}
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", c.apiBase, c.repo, netURL.PathEscape(tag))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to build release request for %s: %w", tag, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Debug("Failed to fetch release by tag", zap.String("tag", tag), zap.Error(err))
		return nil, fmt.Errorf("failed to fetch release %s: %w", tag, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s not found", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for release %s", resp.StatusCode, tag)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release %s: %w", tag, err)
	}
	return &release, nil
}
