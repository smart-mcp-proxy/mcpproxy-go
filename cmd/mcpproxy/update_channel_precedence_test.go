package main

import (
	"testing"
)

// prereleasePreference must apply the same build-version-authoritative rule as
// the daemon's Checker.IncludePrereleases (Spec 079 FR-014 / FR-023): a stable
// build never resolves against the prerelease list, whatever a stale
// `channel: rc` config says, and an RC build always does.
func TestPrereleasePreference_BuildIdentityAuthoritative(t *testing.T) {
	// Pin the env opt-in off so ambient shell exports (an RC dogfooder's
	// MCPPROXY_ALLOW_PRERELEASE_UPDATES=true) can't flip the negative cases.
	t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "")

	t.Run("stable build ignores stale rc config", func(t *testing.T) {
		if prereleasePreferenceFor("v0.60.0", true) {
			t.Fatal("stable build must never offer prereleases, even with channel: rc config")
		}
	})

	t.Run("stable build ignores env opt-in", func(t *testing.T) {
		t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "true")
		if prereleasePreferenceFor("v0.60.0", false) {
			t.Fatal("stable build must never offer prereleases, even with env opt-in")
		}
	})

	t.Run("rc build always tracks prereleases", func(t *testing.T) {
		if !prereleasePreferenceFor("v0.61.0-rc.1", false) {
			t.Fatal("rc build must track the rc channel regardless of config")
		}
	})

	t.Run("dev build honors config opt-in", func(t *testing.T) {
		if !prereleasePreferenceFor("development", true) {
			t.Fatal("unstamped build with channel: rc must opt in")
		}
	})

	t.Run("dev build honors env opt-in", func(t *testing.T) {
		t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "true")
		if !prereleasePreferenceFor("development", false) {
			t.Fatal("unstamped build with env opt-in must opt in")
		}
	})

	t.Run("dev build defaults to stable", func(t *testing.T) {
		if prereleasePreferenceFor("development", false) {
			t.Fatal("unstamped build without any opt-in must stay on stable")
		}
	})
}
