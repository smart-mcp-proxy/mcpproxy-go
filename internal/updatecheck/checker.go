package updatecheck

import (
	"context"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	// DefaultCheckInterval is the default interval between update checks.
	// At most a daily check (Spec 079 FR-018): unauthenticated GitHub API
	// calls are rate-limited and update awareness does not need to be fresher
	// than a day.
	DefaultCheckInterval = 24 * time.Hour

	// maxBackoffFactor caps the failure backoff at 2^3 = 8x the check
	// interval (FR-018: back off on failure, treat errors as "unknown").
	maxBackoffFactor = 3

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

	// installChannel is the distribution channel detected once at New()
	// (Spec 079 FR-008); stamped onto every VersionInfo so /api/v1/info
	// always carries install_channel, even with no update available.
	installChannel string

	// announcedVersion is the latest version already announced at Info level.
	// It dedupes the "Update available" log so a given version is announced
	// exactly once per process, not on every periodic tick (Spec 079 FR-004).
	announcedVersion string

	// cfgEnabled / cfgPrerelease mirror the update_check config block (Spec
	// 079 FR-012): enabled (default true) and channel ("rc" ⇒ prereleases).
	// The environment switches win over them — see Enabled /
	// IncludePrereleases. Mutated by SetConfig on config hot-reload.
	cfgEnabled    bool
	cfgPrerelease bool

	// started/startCtx record that the background loop is running so a
	// SetConfig re-enable can trigger a prompt re-check instead of waiting
	// up to a full checkInterval.
	started  bool
	startCtx context.Context

	// cfgGen is bumped on every effective SetConfig change. A check captures
	// the generation it started under and its result is dropped if the config
	// changed while it was in flight, so a disable or channel switch can never
	// be raced by a stale publish/announce (FR-013/FR-015).
	cfgGen uint64

	// consecutiveFailures / nextCheckAt implement the failure backoff (Spec
	// 079 FR-018): each consecutive failed check doubles the wait before the
	// periodic loop may hit the network again (capped at 2^maxBackoffFactor
	// intervals). Zero nextCheckAt = no restriction (ticker cadence governs).
	// CheckNow (user-initiated refresh) bypasses the window.
	consecutiveFailures int
	nextCheckAt         time.Time

	// nudgesSuppressed is detected once at New() (Spec 079 FR-019): in CI /
	// non-interactive contexts UI surfaces must stay quiet while
	// machine-readable fields still report the facts. Stamped onto every
	// VersionInfo.
	nudgesSuppressed bool

	// nowFn is the clock; overridden in tests.
	nowFn func() time.Time

	// For testing: allows injection of a custom check function
	checkFunc func() (*GitHubRelease, error)
}

// isQuietEnvironment reports whether UI nudges should be suppressed (Spec 079
// FR-019): CI pipelines set CI=true/1 (the same convention the telemetry
// env-filter uses); such runs are non-interactive and a per-run availability
// nag would only spam logs.
func isQuietEnvironment() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CI")))
	return v == "true" || v == "1"
}

// New creates a new update checker.
func New(logger *zap.Logger, version string) *Checker {
	githubClient := NewGitHubClient(logger)

	installChannel := DetectChannel(version)
	// go-install builds carry no ldflags version; promote the build-info
	// module version so isValidSemver does not disable checks for them
	// (see promoteGoInstallVersion).
	version = promoteGoInstallVersion(version, installChannel, debug.ReadBuildInfo)

	nudgesSuppressed := isQuietEnvironment()
	c := &Checker{
		logger:           logger,
		version:          version,
		checkInterval:    DefaultCheckInterval,
		githubClient:     githubClient,
		cfgEnabled:       true, // update_check.enabled defaults to true (Spec 079 FR-012)
		installChannel:   installChannel,
		nudgesSuppressed: nudgesSuppressed,
		nowFn:            time.Now,
		versionInfo: &VersionInfo{
			CurrentVersion:   version,
			InstallChannel:   installChannel,
			NudgesSuppressed: nudgesSuppressed,
		},
	}

	// Default check function uses GitHub client; the channel is resolved per
	// check so a config hot-reload (stable ⇄ rc) takes effect immediately.
	c.checkFunc = func() (*GitHubRelease, error) {
		return c.githubClient.GetRelease(c.IncludePrereleases())
	}

	return c
}

// SetConfig applies the update_check config block (Spec 079 FR-012):
// enabled gates both the background poll and CheckNow; includePrereleases
// selects the release channel (stable vs rc).
//
// Precedence (FR-014, documented in docs/features/version-updates.md): the
// environment switches win over config — MCPPROXY_DISABLE_AUTO_UPDATE=true
// force-disables even with enabled=true, and
// MCPPROXY_ALLOW_PRERELEASE_UPDATES=true force-includes prereleases even with
// channel=stable. The spec leaves precedence open; env-wins is the safe
// operator-override reading.
//
// Safe to call at any time (config hot-reload). When the background loop is
// running, a change that leaves checking enabled (re-enable or channel
// switch) triggers a prompt background re-check instead of waiting up to a
// full checkInterval.
func (c *Checker) SetConfig(enabled, includePrereleases bool) {
	c.mu.Lock()
	changed := c.cfgEnabled != enabled || c.cfgPrerelease != includePrereleases
	c.cfgEnabled = enabled
	c.cfgPrerelease = includePrereleases
	started := c.started
	ctx := c.startCtx
	if changed {
		c.cfgGen++
		// Drop results cached under the previous config so a re-enable or a
		// channel switch never briefly serves stale (possibly wrong-channel)
		// info before the prompt re-check completes (FR-013).
		c.versionInfo = &VersionInfo{CurrentVersion: c.version, InstallChannel: c.installChannel, NudgesSuppressed: c.nudgesSuppressed}
		// A pre-change failure must not impose its backoff window (FR-018) on
		// the new configuration — the prompt re-enable/channel-switch check
		// below must actually run.
		c.consecutiveFailures = 0
		c.nextCheckAt = time.Time{}
	}
	c.mu.Unlock()

	if !changed {
		return
	}
	if !enabled {
		c.logger.Info("Update checks disabled by config (update_check.enabled=false)")
		return
	}
	c.logger.Info("Update check config applied",
		zap.Bool("enabled", enabled),
		zap.Bool("include_prereleases", includePrereleases))
	if started && ctx != nil && ctx.Err() == nil && c.Enabled() {
		go c.check()
	}
}

// Enabled reports whether update checking is effectively enabled: the
// MCPPROXY_DISABLE_AUTO_UPDATE environment kill-switch wins over the config
// value (Spec 079 FR-014 precedence: env > config).
func (c *Checker) Enabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.enabledLocked()
}

// enabledLocked mirrors Enabled for callers already holding c.mu (RWMutex is
// not reentrant).
func (c *Checker) enabledLocked() bool {
	if os.Getenv(EnvDisableAutoUpdate) == "true" {
		return false
	}
	return c.cfgEnabled
}

// IncludePrereleases reports whether prerelease versions are offered:
// MCPPROXY_ALLOW_PRERELEASE_UPDATES=true wins over the config channel
// (Spec 079 FR-014 precedence: env > config); otherwise channel=rc opts in.
func (c *Checker) IncludePrereleases() bool {
	// The running build's own version is authoritative (issue: a stable user
	// must never be offered an RC; an RC user may be offered stable or the next
	// RC). This deliberately overrides the config/env opt-in for RELEASED
	// builds, so a stale `channel: rc` config left over from a
	// previously-installed RC cannot resurrect RC offers on a stable build.
	switch versionChannelKind(c.version) {
	case buildChannelStable:
		// A stable build never tracks prereleases, whatever the config/env say.
		return false
	case buildChannelPrerelease:
		// An RC build tracks the rc channel: it is offered the next RC, and —
		// via pure-semver comparison (compareVersions) — its graduating stable.
		return true
	default:
		// buildChannelUnknown: a build with no RELEASE identity — an unstamped
		// local build ("development"/"dev", which the Start/CheckNow semver
		// guard skips entirely) or a `go install @commit` pseudo-version (valid
		// semver, so it DOES check). Neither is an -rc.*/-next.* release, so the
		// config/env opt-in still applies — the only way to exercise the rc
		// channel without a released RC binary.
		if os.Getenv(EnvAllowPrereleaseUpdates) == "true" {
			return true
		}
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.cfgPrerelease
	}
}

// buildChannelKind classifies the running build's own version.
type buildChannelKind int

const (
	// buildChannelUnknown is an unstamped/dev build (invalid semver, e.g.
	// "development") with no release identity — the config/env opt-in applies.
	buildChannelUnknown buildChannelKind = iota
	// buildChannelStable is a released stable build (valid semver, no
	// prerelease suffix) — never offered a prerelease.
	buildChannelStable
	// buildChannelPrerelease is a released RC build (valid semver with an
	// -rc.N / -next.* suffix) — tracks the rc channel.
	buildChannelPrerelease
)

// versionChannelKind classifies a build version for channel selection.
func versionChannelKind(version string) buildChannelKind {
	v := ensureVPrefix(version)
	if !semver.IsValid(v) {
		return buildChannelUnknown
	}
	// A `go install @commit` pseudo-version (e.g. v0.47.1-0.20260701123456-abc)
	// is valid semver WITH a prerelease component, but it is a development
	// build off an arbitrary commit — not an -rc.*/-next.* release. Classify it
	// as unknown so it is not force-tracked onto the rc channel; the config/env
	// opt-in governs it like any other dev build.
	if module.IsPseudoVersion(v) {
		return buildChannelUnknown
	}
	if semver.Prerelease(v) != "" {
		return buildChannelPrerelease
	}
	return buildChannelStable
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

	c.mu.Lock()
	c.started = true
	c.startCtx = ctx
	c.mu.Unlock()

	// Perform initial check in a separate goroutine to avoid blocking startup.
	// When disabled by config the loop stays alive but idle, so a hot-reload
	// flip to enabled=true resumes checking without a restart (Spec 079
	// FR-012); SetConfig triggers the prompt re-check on that transition.
	if c.Enabled() {
		go c.check()
	} else {
		c.logger.Info("Update checks disabled by config; loop idle until re-enabled (update_check.enabled)")
	}

	// Start periodic checks
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Update checker stopped")
			return
		case <-ticker.C:
			if c.Enabled() {
				c.check()
			}
		}
	}
}

// GetVersionInfo returns the current version information.
// Thread-safe. Returns nil when update checking is disabled (config or env):
// with no check running there is no meaningful update state, and FR-015
// requires zero nudge on every surface — /api/v1/info then omits the update
// object entirely, so the Web UI banner/badge and CLI annotations naturally
// disappear.
func (c *Checker) GetVersionInfo() *VersionInfo {
	if !c.Enabled() {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.versionInfo == nil {
		return &VersionInfo{
			CurrentVersion: c.version,
			InstallChannel: c.installChannel,
		}
	}

	// Return a copy to prevent external modification
	info := *c.versionInfo
	return &info
}

// check performs a single periodic update check against GitHub, honoring the
// failure backoff window (FR-018). User-initiated refreshes go through
// CheckNow, which bypasses the window.
func (c *Checker) check() {
	c.runCheck(false)
}

// runCheck performs a single update check. force=true (CheckNow) bypasses the
// failure-backoff window; the periodic loop passes false.
func (c *Checker) runCheck(force bool) {
	c.logger.Debug("Checking for updates")

	c.mu.RLock()
	gen := c.cfgGen
	enabled := c.enabledLocked()
	nextCheckAt := c.nextCheckAt
	c.mu.RUnlock()

	// Re-read enabled under the same lock as gen: a disable racing after the
	// caller's outer Enabled() gate (loop tick, or a re-enable-triggered
	// goroutine from SetConfig) must not fire a network request. The
	// generation guard already drops any stale result; this avoids the request
	// entirely (FR-015: disabled means no network check on any surface).
	if !enabled {
		c.logger.Debug("Skipping update check: update checking disabled")
		return
	}

	// Failure backoff (FR-018): after a failed check the periodic loop stays
	// off the network until the window expires; errors are "unknown", not a
	// reason to retry aggressively against a rate-limited API.
	if !force && !nextCheckAt.IsZero() && c.nowFn().Before(nextCheckAt) {
		c.logger.Debug("Skipping update check: backing off after failure",
			zap.Time("next_check_at", nextCheckAt))
		return
	}

	release, err := c.checkFunc()
	if err != nil {
		c.mu.Lock()
		// Generation guard (mirrors updateVersionInfo): a failure from a
		// check that raced a SetConfig change must not impose backoff on the
		// new configuration.
		if gen == c.cfgGen {
			c.consecutiveFailures++
			factor := c.consecutiveFailures
			if factor > maxBackoffFactor {
				factor = maxBackoffFactor
			}
			c.nextCheckAt = c.nowFn().Add(c.checkInterval * (1 << factor))
		}
		c.mu.Unlock()
		c.logger.Debug("Update check failed", zap.Error(err))
		c.updateVersionInfo(nil, err.Error(), gen)
		return
	}

	c.mu.Lock()
	if gen == c.cfgGen {
		c.consecutiveFailures = 0
		c.nextCheckAt = time.Time{}
	}
	c.mu.Unlock()
	c.updateVersionInfo(release, "", gen)
}

// updateVersionInfo updates the cached version information. gen is the config
// generation the check started under; results from a check that raced a
// SetConfig change (disable, channel switch) are dropped so nothing stale is
// published or announced after the change (FR-013/FR-015).
func (c *Checker) updateVersionInfo(release *GitHubRelease, checkError string, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if gen != c.cfgGen || !c.enabledLocked() {
		c.logger.Debug("Discarding update-check result from a stale config generation")
		return
	}

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

	// update_command only accompanies an actual update on channels with a
	// safe one-line command (Spec 079 FR-009; empty for dmg/windows-installer/
	// tarball/docker/unknown — surfaces render guidance text instead).
	// Prerelease targets are special: the package-manager channels only serve
	// stable artifacts, so their generic commands would not deliver the
	// advertised rc — only go-install gets a version-pinned command
	// (PrereleaseUpdateCommand).
	updateCommand := ""
	if updateAvailable {
		if release.Prerelease {
			updateCommand = PrereleaseUpdateCommand(c.installChannel, latestVersion)
		} else {
			updateCommand = UpdateCommand(c.installChannel)
		}
	}

	c.versionInfo = &VersionInfo{
		CurrentVersion:   c.version,
		LatestVersion:    latestVersion,
		UpdateAvailable:  updateAvailable,
		ReleaseURL:       release.HTMLURL,
		CheckedAt:        &now,
		IsPrerelease:     release.Prerelease,
		CheckError:       "",
		InstallChannel:   c.installChannel,
		UpdateCommand:    updateCommand,
		NudgesSuppressed: c.nudgesSuppressed,
	}

	switch {
	// FR-019: in CI / non-interactive contexts the per-run availability
	// announcement is a log nag — keep the facts machine-readable only.
	case updateAvailable && c.nudgesSuppressed:
		c.logger.Debug("Update available (nudges suppressed: CI/non-interactive)",
			zap.String("current", c.version),
			zap.String("latest", latestVersion))
	case updateAvailable && latestVersion != c.announcedVersion:
		// Announce each newly detected version exactly once per process;
		// subsequent ticks for the same version log at Debug only.
		// TODO(spec-079/FR-002): include the "N releases / M weeks behind"
		// delta here once the checker fetches the release list + publish
		// dates (a later 079 slice extending VersionInfo, additive per
		// FR-021).
		c.announcedVersion = latestVersion
		c.logger.Info("Update available",
			zap.String("current", c.version),
			zap.String("latest", latestVersion),
			zap.String("url", release.HTMLURL))
	case updateAvailable:
		c.logger.Debug("Update still available",
			zap.String("current", c.version),
			zap.String("latest", latestVersion))
	default:
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

	// semver.Compare ranks an invalid version below every valid one, which
	// would flag any published release as an "update" for a dev build.
	if !semver.IsValid(currentSemver) {
		return false
	}

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

// CheckNow performs an immediate update check against GitHub.
// This bypasses the periodic check interval and updates the cached version info.
// Returns the updated VersionInfo after the check completes.
// When update checking is disabled (update_check.enabled=false or
// MCPPROXY_DISABLE_AUTO_UPDATE=true) no network check is performed and nil is
// returned (Spec 079 FR-015: disabled means no check and no nudge anywhere,
// including the manual /api/v1/info?refresh=true path).
func (c *Checker) CheckNow() *VersionInfo {
	if !c.Enabled() {
		c.logger.Debug("Immediate update check skipped: update checking disabled")
		return nil
	}
	// Same guard as Start(): a non-semver current version (e.g. "development")
	// cannot be meaningfully compared, and offering the latest stable release
	// against it is a downgrade prompt, not an update.
	if !c.isValidSemver() {
		c.logger.Debug("Immediate update check skipped: non-semver version",
			zap.String("version", c.version))
		return nil
	}
	c.logger.Debug("Performing immediate update check")
	// force=true: an explicit user refresh bypasses the failure-backoff
	// window (FR-018 applies to the automated cadence, not manual intent).
	c.runCheck(true)
	return c.GetVersionInfo()
}
