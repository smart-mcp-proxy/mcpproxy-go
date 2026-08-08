package updatecheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

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

// GetLatestReleaseIncludingPrereleases fetches the latest release including prereleases.
func (c *GitHubClient) GetLatestReleaseIncludingPrereleases() (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases", c.apiBase, c.repo)

	resp, err := c.httpClient.Get(url) // #nosec G107 -- URL is constructed from known repo constant
	if err != nil {
		c.logger.Debug("Failed to fetch releases list", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

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

	// Return the first release (GitHub returns them sorted by creation date, newest first)
	return &releases[0], nil
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
	if tag == "" {
		return nil, fmt.Errorf("release tag is required")
	}
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", c.apiBase, c.repo, tag)

	resp, err := c.httpClient.Get(url) // #nosec G107 -- base is a constant or test-injected; tag is validated by the caller
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
