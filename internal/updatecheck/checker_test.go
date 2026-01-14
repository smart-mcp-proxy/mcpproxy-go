package updatecheck

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
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
		return // unreachable but satisfies staticcheck SA5011
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
		return // unreachable but satisfies staticcheck SA5011
	}

	if info.UpdateAvailable {
		t.Error("UpdateAvailable = true, want false (same version)")
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
		{"1.0.0", "1.1.0", true},  // Without v prefix
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
