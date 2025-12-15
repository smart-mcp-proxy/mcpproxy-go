package updatecheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	// GitHubRepo is the repository to check for releases
	GitHubRepo = "smart-mcp-proxy/mcpproxy-go"

	// httpTimeout is the timeout for GitHub API requests
	httpTimeout = 10 * time.Second
)

// GitHubClient handles communication with the GitHub Releases API.
type GitHubClient struct {
	logger     *zap.Logger
	httpClient *http.Client
	repo       string
}

// NewGitHubClient creates a new GitHub API client.
func NewGitHubClient(logger *zap.Logger) *GitHubClient {
	return &GitHubClient{
		logger: logger,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
		repo: GitHubRepo,
	}
}

// GetLatestRelease fetches the latest stable release from GitHub.
func (c *GitHubClient) GetLatestRelease() (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.repo)

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
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", c.repo)

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
