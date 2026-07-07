package updatecheck

import (
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
)

// setTestChannel overrides the install channel detected at New() time so
// checker-level tests are independent of the machine they run on.
func setTestChannel(c *Checker, channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.installChannel = channel
	if c.versionInfo != nil {
		c.versionInfo.InstallChannel = channel
	}
}

func TestChecker_InstallChannelAlwaysSet(t *testing.T) {
	checker := New(zap.NewNop(), "v1.0.0")
	setTestChannel(checker, ChannelHomebrew)

	// Before any check completes the channel is already known (FR-021:
	// install_channel is set even with no update).
	info := checker.GetVersionInfo()
	if info == nil {
		t.Fatal("expected version info")
	}
	if info.InstallChannel != ChannelHomebrew {
		t.Errorf("InstallChannel = %q, want %q", info.InstallChannel, ChannelHomebrew)
	}
	if info.UpdateCommand != "" {
		t.Errorf("UpdateCommand = %q, want empty before any check", info.UpdateCommand)
	}

	// Up to date: channel still present, no command.
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.0.0", HTMLURL: "https://example.com/v1.0.0"}, nil
	})
	info = checker.CheckNow()
	if info == nil {
		t.Fatal("expected version info")
	}
	if info.InstallChannel != ChannelHomebrew {
		t.Errorf("InstallChannel = %q, want %q after up-to-date check", info.InstallChannel, ChannelHomebrew)
	}
	if info.UpdateCommand != "" {
		t.Errorf("UpdateCommand = %q, want empty when no update available", info.UpdateCommand)
	}
}

func TestChecker_UpdateCommandOnlyWhenUpdateAvailable(t *testing.T) {
	checker := New(zap.NewNop(), "v1.0.0")
	setTestChannel(checker, ChannelHomebrew)
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.1.0", HTMLURL: "https://example.com/v1.1.0"}, nil
	})

	info := checker.CheckNow()
	if info == nil {
		t.Fatal("expected version info")
	}
	if !info.UpdateAvailable {
		t.Fatal("expected update available")
	}
	if info.UpdateCommand != "brew upgrade mcpproxy" {
		t.Errorf("UpdateCommand = %q, want %q", info.UpdateCommand, "brew upgrade mcpproxy")
	}
	if info.InstallChannel != ChannelHomebrew {
		t.Errorf("InstallChannel = %q, want %q", info.InstallChannel, ChannelHomebrew)
	}
}

func TestChecker_NoCommandChannelGetsNoCommandEvenWhenBehind(t *testing.T) {
	checker := New(zap.NewNop(), "v1.0.0")
	setTestChannel(checker, ChannelDMG)
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v1.1.0", HTMLURL: "https://example.com/v1.1.0"}, nil
	})

	info := checker.CheckNow()
	if info == nil || !info.UpdateAvailable {
		t.Fatal("expected update available")
	}
	if info.UpdateCommand != "" {
		t.Errorf("UpdateCommand = %q, want empty for dmg channel", info.UpdateCommand)
	}
	if info.InstallChannel != ChannelDMG {
		t.Errorf("InstallChannel = %q, want %q", info.InstallChannel, ChannelDMG)
	}
}

// Spec 079 edge case (spec.md L82), documented interpretation: pure semver
// comparison stands. A prerelease AHEAD of the latest stable is never nudged
// "up" (that would be a downgrade); a prerelease BEHIND the latest stable IS
// nudged to that stable (an rc user finishing the cycle should land on the
// released stable).
func TestChecker_PrereleaseOnStableChannelPins(t *testing.T) {
	t.Run("rc ahead of latest stable: no nudge, no command", func(t *testing.T) {
		checker := New(zap.NewNop(), "v0.48.0-rc.1")
		setTestChannel(checker, ChannelHomebrew)
		checker.SetCheckFunc(func() (*GitHubRelease, error) {
			return &GitHubRelease{TagName: "v0.47.0", HTMLURL: "https://example.com/v0.47.0"}, nil
		})

		info := checker.CheckNow()
		if info == nil {
			t.Fatal("expected version info")
		}
		if info.UpdateAvailable {
			t.Error("expected NO nudge for a prerelease newer than latest stable (no downgrade nudge)")
		}
		if info.UpdateCommand != "" {
			t.Errorf("UpdateCommand = %q, want empty", info.UpdateCommand)
		}
	})

	t.Run("rc behind its own stable: nudge to stable", func(t *testing.T) {
		checker := New(zap.NewNop(), "v0.47.0-rc.4")
		setTestChannel(checker, ChannelHomebrew)
		checker.SetCheckFunc(func() (*GitHubRelease, error) {
			return &GitHubRelease{TagName: "v0.47.0", HTMLURL: "https://example.com/v0.47.0"}, nil
		})

		info := checker.CheckNow()
		if info == nil {
			t.Fatal("expected version info")
		}
		if !info.UpdateAvailable {
			t.Error("expected nudge from v0.47.0-rc.4 to stable v0.47.0")
		}
		if info.UpdateCommand != "brew upgrade mcpproxy" {
			t.Errorf("UpdateCommand = %q, want %q", info.UpdateCommand, "brew upgrade mcpproxy")
		}
	})
}

// FR-021 contract: adding install_channel/update_command must not remove or
// rename any of the six existing update fields.
func TestToAPIResponse_ExistingFieldsSurvive(t *testing.T) {
	now := time.Now()
	info := &VersionInfo{
		CurrentVersion:  "v1.0.0",
		LatestVersion:   "v1.1.0",
		UpdateAvailable: true,
		ReleaseURL:      "https://example.com/v1.1.0",
		CheckedAt:       &now,
		IsPrerelease:    true,
		CheckError:      "transient",
		InstallChannel:  ChannelHomebrew,
		UpdateCommand:   "brew upgrade mcpproxy",
	}

	data, err := json.Marshal(info.ToAPIResponse())
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	for _, key := range []string{
		// The six pre-existing fields (FR-021: MUST NOT remove or repurpose).
		"available", "latest_version", "release_url", "checked_at", "is_prerelease", "check_error",
		// The two additive Spec 079 US2 fields.
		"install_channel", "update_command",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected JSON key %q in update payload, got keys %v", key, rawKeys(raw))
		}
	}

	if raw["install_channel"] != ChannelHomebrew {
		t.Errorf("install_channel = %v, want %q", raw["install_channel"], ChannelHomebrew)
	}
	if raw["update_command"] != "brew upgrade mcpproxy" {
		t.Errorf("update_command = %v, want %q", raw["update_command"], "brew upgrade mcpproxy")
	}
}

func rawKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
