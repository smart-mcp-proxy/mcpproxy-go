package updatecheck

import (
	"testing"

	"go.uber.org/zap"
)

// The running build's own version is authoritative for the update channel: a
// STABLE build must never be offered a prerelease (even if a stale rc config
// or the env flag lingers from a previously-installed RC), while an RC build
// tracks the rc channel so it is offered the next RC (or its graduating
// stable). A dev/unstamped build keeps the config/env opt-in so local testing
// can still exercise the rc path.
func TestChecker_IncludePrereleases_BuildIdentityAuthoritative(t *testing.T) {
	t.Run("stable build ignores config rc", func(t *testing.T) {
		c := New(zap.NewNop(), "v1.2.3")
		c.SetConfig(true, true) // config asks for rc
		if c.IncludePrereleases() {
			t.Error("stable build must never include prereleases, even with config channel=rc")
		}
	})

	t.Run("stable build ignores env flag", func(t *testing.T) {
		t.Setenv(EnvAllowPrereleaseUpdates, "true")
		c := New(zap.NewNop(), "v1.2.3")
		if c.IncludePrereleases() {
			t.Error("stable build must never include prereleases, even with the env override")
		}
	})

	t.Run("rc build includes prereleases by default", func(t *testing.T) {
		c := New(zap.NewNop(), "v1.3.0-rc.1")
		// No config/env opt-in — build identity alone puts it on the rc track.
		if !c.IncludePrereleases() {
			t.Error("an rc build must track the rc channel by default")
		}
	})

	t.Run("dev build honors config opt-in", func(t *testing.T) {
		c := New(zap.NewNop(), "development")
		if c.IncludePrereleases() {
			t.Error("dev build defaults to stable")
		}
		c.SetConfig(true, true)
		if !c.IncludePrereleases() {
			t.Error("dev build must honor config channel=rc")
		}
	})

	t.Run("dev build honors env opt-in", func(t *testing.T) {
		t.Setenv(EnvAllowPrereleaseUpdates, "true")
		c := New(zap.NewNop(), "development")
		if !c.IncludePrereleases() {
			t.Error("dev build must honor the env override")
		}
	})

	t.Run("go-install pseudo-version is dev, not RC", func(t *testing.T) {
		// A `go install @commit` pseudo-version is valid semver with a
		// prerelease component but is NOT a released RC — it must not be
		// force-tracked onto the rc channel; the opt-in governs it.
		pseudo := "v0.47.1-0.20260701123456-abcdef123456"
		c := New(zap.NewNop(), pseudo)
		if c.IncludePrereleases() {
			t.Error("pseudo-version must default to stable (not forced onto rc like a real -rc build)")
		}
		c.SetConfig(true, true)
		if !c.IncludePrereleases() {
			t.Error("pseudo-version must honor config channel=rc (treated as a dev build)")
		}
	})
}

// Policy().Channel is derived from IncludePrereleases(), so the same
// build-identity clamp must surface in the policy the tray consumes over
// /api/v1/info — a stable build reports channel=stable regardless of config,
// an rc build reports channel=rc.
func TestChecker_Policy_ChannelFollowsBuildIdentity(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv(EnvDisableAutoUpdate, "")
	t.Setenv(EnvAllowPrereleaseUpdates, "")

	t.Run("stable build reports stable despite config rc", func(t *testing.T) {
		c := New(zap.NewNop(), "v1.2.3")
		c.SetConfig(true, true)
		if got := c.Policy().Channel; got != PolicyChannelStable {
			t.Fatalf("stable build channel = %q, want %q", got, PolicyChannelStable)
		}
	})

	t.Run("rc build reports rc", func(t *testing.T) {
		c := New(zap.NewNop(), "v1.3.0-rc.2")
		if got := c.Policy().Channel; got != PolicyChannelRC {
			t.Fatalf("rc build channel = %q, want %q", got, PolicyChannelRC)
		}
	})
}
