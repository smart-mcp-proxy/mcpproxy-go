package updatecheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

func TestChecker_CheckNow(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create checker with a valid semver version
	checker := New(logger, "v1.0.0")

	// Set up a mock check function to avoid hitting GitHub
	mockRelease := &GitHubRelease{
		TagName:    "v1.1.0",
		HTMLURL:    "https://github.com/test/repo/releases/tag/v1.1.0",
		Prerelease: false,
	}
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return mockRelease, nil
	})

	// Test CheckNow returns version info
	info := checker.CheckNow()

	if info == nil {
		t.Fatal("CheckNow returned nil")
	}

	if info.CurrentVersion != "v1.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", info.CurrentVersion, "v1.0.0")
	}

	if info.LatestVersion != "v1.1.0" {
		t.Errorf("LatestVersion = %q, want %q", info.LatestVersion, "v1.1.0")
	}

	if !info.UpdateAvailable {
		t.Error("UpdateAvailable = false, want true")
	}

	if info.ReleaseURL != "https://github.com/test/repo/releases/tag/v1.1.0" {
		t.Errorf("ReleaseURL = %q, want %q", info.ReleaseURL, "https://github.com/test/repo/releases/tag/v1.1.0")
	}
}

func TestChecker_CheckNow_NoUpdate(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create checker with a valid semver version
	checker := New(logger, "v1.1.0")

	// Set up a mock check function
	mockRelease := &GitHubRelease{
		TagName:    "v1.1.0",
		HTMLURL:    "https://github.com/test/repo/releases/tag/v1.1.0",
		Prerelease: false,
	}
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return mockRelease, nil
	})

	// Test CheckNow returns version info with no update
	info := checker.CheckNow()

	if info == nil {
		t.Fatal("CheckNow returned nil")
	}

	if info.UpdateAvailable {
		t.Error("UpdateAvailable = true, want false (same version)")
	}
}

// TestChecker_UpdateAvailableLoggedOncePerVersion verifies the "Update available"
// Info log is emitted exactly once per detected latest version, not on every
// periodic tick (FR-004 of specs/079-upgrade-nudge: no repeated log spam).
func TestChecker_UpdateAvailableLoggedOncePerVersion(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	checker := New(logger, "v1.0.0")
	release := &GitHubRelease{
		TagName: "v1.1.0",
		HTMLURL: "https://github.com/test/repo/releases/tag/v1.1.0",
	}
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return release, nil
	})

	// Simulate the initial check plus two periodic ticks for the same version.
	checker.check()
	checker.check()
	checker.check()

	if got := logs.FilterMessage("Update available").Len(); got != 1 {
		t.Errorf("expected exactly 1 'Update available' Info log for the same version, got %d", got)
	}

	// A transient failure followed by recovery to the same version must not re-announce.
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return nil, errors.New("network unreachable")
	})
	checker.check()
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return release, nil
	})
	checker.check()

	if got := logs.FilterMessage("Update available").Len(); got != 1 {
		t.Errorf("expected still 1 'Update available' Info log after error+recovery, got %d", got)
	}

	// A newer latest version must be announced once more.
	newer := &GitHubRelease{
		TagName: "v1.2.0",
		HTMLURL: "https://github.com/test/repo/releases/tag/v1.2.0",
	}
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return newer, nil
	})
	checker.check()
	checker.check()

	if got := logs.FilterMessage("Update available").Len(); got != 2 {
		t.Errorf("expected 2 'Update available' Info logs after a newer version appeared, got %d", got)
	}
}

// TestChecker_NoUpdateLogWhenCurrent verifies no Info-level nudge is logged
// when the running version is already the latest.
func TestChecker_NoUpdateLogWhenCurrent(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	checker := New(logger, "v1.1.0")
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})

	checker.check()
	checker.check()

	if got := logs.FilterMessage("Update available").Len(); got != 0 {
		t.Errorf("expected no 'Update available' Info log when current, got %d", got)
	}
}

func TestChecker_CompareVersions(t *testing.T) {
	logger := zap.NewNop()
	checker := New(logger, "v1.0.0")

	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.0.0", "v1.1.0", true},
		{"v1.1.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.0", false},
		{"1.0.0", "1.1.0", true}, // Without v prefix
		{"v0.11.1", "v0.11.3", true},
		{"v0.11.2", "v0.11.2", false},
	}

	for _, tc := range tests {
		t.Run(tc.current+"_vs_"+tc.latest, func(t *testing.T) {
			got := checker.compareVersions(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("compareVersions(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

// --- Spec 079 US1: update_check config block (FR-012/FR-014/FR-015) ---

// TestChecker_SetConfig_DisabledSkipsCheckAndHidesInfo verifies that
// update_check.enabled=false gates BOTH the manual CheckNow path and the
// surfaced version info: no network check runs and GetVersionInfo returns nil
// so /api/v1/info omits the update object entirely (FR-015).
func TestChecker_SetConfig_DisabledSkipsCheckAndHidesInfo(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")

	calls := 0
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		calls++
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})

	checker.SetConfig(false, false)

	if got := checker.CheckNow(); got != nil {
		t.Errorf("CheckNow() = %+v, want nil when disabled by config", got)
	}
	if calls != 0 {
		t.Errorf("check function invoked %d times, want 0 when disabled", calls)
	}
	if got := checker.GetVersionInfo(); got != nil {
		t.Errorf("GetVersionInfo() = %+v, want nil when disabled by config", got)
	}
	if checker.Enabled() {
		t.Error("Enabled() = true, want false after SetConfig(false, ...)")
	}
}

// TestChecker_SetConfig_ReEnableRestoresChecks verifies a hot-reload flip back
// to enabled=true makes CheckNow work again without a restart (FR-012).
func TestChecker_SetConfig_ReEnableRestoresChecks(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})

	checker.SetConfig(false, false)
	if checker.CheckNow() != nil {
		t.Fatal("expected nil CheckNow while disabled")
	}

	checker.SetConfig(true, false)
	info := checker.CheckNow()
	if info == nil {
		t.Fatal("CheckNow() = nil after re-enable, want version info")
	}
	if !info.UpdateAvailable || info.LatestVersion != "v1.1.0" {
		t.Errorf("unexpected info after re-enable: %+v", info)
	}
}

// TestChecker_EnvDisableWinsOverConfig verifies the documented precedence
// (FR-014): the MCPPROXY_DISABLE_AUTO_UPDATE environment kill-switch always
// wins over update_check.enabled=true in the config file.
func TestChecker_EnvDisableWinsOverConfig(t *testing.T) {
	t.Setenv(EnvDisableAutoUpdate, "true")

	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetConfig(true, false)

	if checker.Enabled() {
		t.Error("Enabled() = true, want false: env kill-switch must win over config")
	}
	calls := 0
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		calls++
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})
	if got := checker.CheckNow(); got != nil {
		t.Errorf("CheckNow() = %+v, want nil when env-disabled", got)
	}
	if calls != 0 {
		t.Errorf("check function invoked %d times, want 0 when env-disabled", calls)
	}
}

// TestChecker_IncludePrereleases_Resolution verifies channel resolution:
// default stable, config channel=rc opts in, and the
// MCPPROXY_ALLOW_PRERELEASE_UPDATES env override wins over a stable config
// channel (FR-013/FR-014).
func TestChecker_IncludePrereleases_Resolution(t *testing.T) {
	t.Run("default is stable", func(t *testing.T) {
		checker := New(zaptest.NewLogger(t), "v1.0.0")
		if checker.IncludePrereleases() {
			t.Error("IncludePrereleases() = true by default, want false (stable channel)")
		}
	})

	t.Run("config rc channel opts in", func(t *testing.T) {
		checker := New(zaptest.NewLogger(t), "v1.0.0")
		checker.SetConfig(true, true)
		if !checker.IncludePrereleases() {
			t.Error("IncludePrereleases() = false, want true after SetConfig(_, true)")
		}
	})

	t.Run("env override wins over stable config", func(t *testing.T) {
		t.Setenv(EnvAllowPrereleaseUpdates, "true")
		checker := New(zaptest.NewLogger(t), "v1.0.0")
		checker.SetConfig(true, false)
		if !checker.IncludePrereleases() {
			t.Error("IncludePrereleases() = false, want true: env override must win")
		}
	})
}

// TestChecker_HotReload_ReEnableTriggersImmediateCheck verifies that when the
// background loop is running but config-disabled, flipping enabled=true via
// hot-reload triggers a prompt re-check instead of waiting up to a full
// 4-hour interval (FR-012 acceptance scenario 1).
func TestChecker_HotReload_ReEnableTriggersImmediateCheck(t *testing.T) {
	// Nop logger: the Start goroutine may log its shutdown line after the
	// test returns, which zaptest would flag as a log-after-test panic.
	checker := New(zap.NewNop(), "v1.0.0")
	checker.SetCheckInterval(time.Hour) // never ticks during the test

	checked := make(chan struct{}, 4)
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		checked <- struct{}{}
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})

	checker.SetConfig(false, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.Start(ctx)

	// Disabled: the loop must not run the initial check.
	select {
	case <-checked:
		t.Fatal("check ran while disabled by config")
	case <-time.After(100 * time.Millisecond):
	}

	checker.SetConfig(true, false)

	select {
	case <-checked:
		// prompt re-check happened
	case <-time.After(2 * time.Second):
		t.Fatal("re-enabling via SetConfig did not trigger a prompt re-check")
	}
}

// TestChecker_SetConfig_ChannelSwitchDropsStaleCache verifies that a config
// change (e.g. rc → stable channel switch) drops previously cached version
// info, so GetVersionInfo never briefly serves a wrong-channel (prerelease)
// result before the re-check completes (FR-013).
func TestChecker_SetConfig_ChannelSwitchDropsStaleCache(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetConfig(true, true) // rc channel
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.1.0-rc.1", Prerelease: true}, nil
	})

	info := checker.CheckNow()
	if info == nil || !info.UpdateAvailable || info.LatestVersion != "v1.1.0-rc.1" {
		t.Fatalf("precondition failed, want cached rc info, got %+v", info)
	}

	// Switch to the stable channel; not started, so no background re-check
	// runs — GetVersionInfo must already be clean.
	checker.SetConfig(true, false)

	got := checker.GetVersionInfo()
	if got == nil {
		t.Fatal("GetVersionInfo() = nil, want fresh (empty) info while enabled")
	}
	if got.UpdateAvailable || got.LatestVersion != "" || got.IsPrerelease {
		t.Errorf("stale cache served after channel switch: %+v", got)
	}
	if got.CurrentVersion != "v1.0.0" {
		t.Errorf("CurrentVersion = %q, want v1.0.0", got.CurrentVersion)
	}
}

// TestChecker_InFlightCheckDiscardedAfterDisable verifies that a check already
// in flight when SetConfig(false) lands neither publishes its result nor emits
// the "Update available" announce log (FR-015: disabled means no nudge on any
// surface, including logs).
func TestChecker_InFlightCheckDiscardedAfterDisable(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	checker := New(zap.New(core), "v1.0.0")

	entered := make(chan struct{})
	release := make(chan struct{})
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		close(entered)
		<-release
		return &GitHubRelease{TagName: "v1.1.0"}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		checker.CheckNow()
	}()

	<-entered
	checker.SetConfig(false, false) // disable while the check is in flight
	close(release)
	<-done

	if got := logs.FilterMessage("Update available").Len(); got != 0 {
		t.Errorf("got %d 'Update available' logs after disable, want 0", got)
	}

	// The stale result must not have been cached (inspect directly: while
	// disabled GetVersionInfo returns nil by design).
	checker.mu.RLock()
	cached := checker.versionInfo
	checker.mu.RUnlock()
	if cached == nil {
		t.Fatal("versionInfo = nil, want cleared placeholder")
	}
	if cached.UpdateAvailable || cached.LatestVersion != "" {
		t.Errorf("in-flight result was published after disable: %+v", cached)
	}
}
