package updatecheck

import (
	"errors"
	"testing"

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
