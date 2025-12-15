package updatecheck

import (
	"context"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/mod/semver"
)

const (
	// DefaultCheckInterval is the default interval between update checks (4 hours).
	DefaultCheckInterval = 4 * time.Hour

	// EnvDisableAutoUpdate disables all update checks when set to "true".
	EnvDisableAutoUpdate = "MCPPROXY_DISABLE_AUTO_UPDATE"

	// EnvAllowPrereleaseUpdates enables prerelease version comparison when set to "true".
	EnvAllowPrereleaseUpdates = "MCPPROXY_ALLOW_PRERELEASE_UPDATES"
)

// Checker performs background version checks against GitHub releases.
type Checker struct {
	logger        *zap.Logger
	version       string
	checkInterval time.Duration
	githubClient  *GitHubClient

	mu          sync.RWMutex
	versionInfo *VersionInfo

	// For testing: allows injection of a custom check function
	checkFunc func() (*GitHubRelease, error)
}

// New creates a new update checker.
func New(logger *zap.Logger, version string) *Checker {
	githubClient := NewGitHubClient(logger)

	c := &Checker{
		logger:        logger,
		version:       version,
		checkInterval: DefaultCheckInterval,
		githubClient:  githubClient,
		versionInfo: &VersionInfo{
			CurrentVersion: version,
		},
	}

	// Default check function uses GitHub client
	c.checkFunc = func() (*GitHubRelease, error) {
		allowPrerelease := os.Getenv(EnvAllowPrereleaseUpdates) == "true"
		return c.githubClient.GetRelease(allowPrerelease)
	}

	return c
}

// Start begins the background update checker.
// It performs an initial check immediately and then checks every checkInterval.
// The checker respects MCPPROXY_DISABLE_AUTO_UPDATE environment variable.
func (c *Checker) Start(ctx context.Context) {
	// Check if auto-update is disabled
	if os.Getenv(EnvDisableAutoUpdate) == "true" {
		c.logger.Info("Update checker disabled by environment variable",
			zap.String("env", EnvDisableAutoUpdate))
		return
	}

	// Skip update checks for development builds
	if !c.isValidSemver() {
		c.logger.Info("Update checker disabled for non-semver version",
			zap.String("version", c.version))
		return
	}

	c.logger.Info("Starting update checker",
		zap.String("version", c.version),
		zap.Duration("interval", c.checkInterval))

	// Perform initial check in a separate goroutine to avoid blocking startup
	go c.check()

	// Start periodic checks
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Update checker stopped")
			return
		case <-ticker.C:
			c.check()
		}
	}
}

// GetVersionInfo returns the current version information.
// Thread-safe.
func (c *Checker) GetVersionInfo() *VersionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.versionInfo == nil {
		return &VersionInfo{
			CurrentVersion: c.version,
		}
	}

	// Return a copy to prevent external modification
	info := *c.versionInfo
	return &info
}

// check performs a single update check against GitHub.
func (c *Checker) check() {
	c.logger.Debug("Checking for updates")

	release, err := c.checkFunc()
	if err != nil {
		c.logger.Debug("Update check failed", zap.Error(err))
		c.updateVersionInfo(nil, err.Error())
		return
	}

	c.updateVersionInfo(release, "")
}

// updateVersionInfo updates the cached version information.
func (c *Checker) updateVersionInfo(release *GitHubRelease, checkError string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	if release == nil {
		// On error, preserve last known state but update error and timestamp
		if c.versionInfo != nil {
			c.versionInfo.CheckedAt = &now
			c.versionInfo.CheckError = checkError
		}
		return
	}

	latestVersion := release.TagName
	updateAvailable := c.compareVersions(c.version, latestVersion)

	c.versionInfo = &VersionInfo{
		CurrentVersion:  c.version,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      release.HTMLURL,
		CheckedAt:       &now,
		IsPrerelease:    release.Prerelease,
		CheckError:      "",
	}

	if updateAvailable {
		c.logger.Info("Update available",
			zap.String("current", c.version),
			zap.String("latest", latestVersion),
			zap.String("url", release.HTMLURL))
	} else {
		c.logger.Debug("Running latest version",
			zap.String("version", c.version))
	}
}

// compareVersions compares current and latest versions using semver.
// Returns true if latest is newer than current.
func (c *Checker) compareVersions(current, latest string) bool {
	// Ensure both versions have "v" prefix for semver comparison
	currentSemver := ensureVPrefix(current)
	latestSemver := ensureVPrefix(latest)

	// semver.Compare returns -1 if current < latest
	return semver.Compare(currentSemver, latestSemver) < 0
}

// isValidSemver checks if the current version is a valid semver string.
// Returns false for development builds like "development" or "dev".
func (c *Checker) isValidSemver() bool {
	v := ensureVPrefix(c.version)
	return semver.IsValid(v)
}

// ensureVPrefix ensures the version string has a "v" prefix for semver comparison.
func ensureVPrefix(version string) string {
	if len(version) > 0 && version[0] != 'v' {
		return "v" + version
	}
	return version
}

// SetCheckInterval sets the interval between update checks.
// Primarily for testing.
func (c *Checker) SetCheckInterval(interval time.Duration) {
	c.checkInterval = interval
}

// SetCheckFunc sets a custom check function.
// Primarily for testing.
func (c *Checker) SetCheckFunc(fn func() (*GitHubRelease, error)) {
	c.checkFunc = fn
}
