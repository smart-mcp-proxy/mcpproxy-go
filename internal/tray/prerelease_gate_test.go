//go:build !nogui && !headless && !linux

package tray

import "testing"

// The legacy tray self-update path must respect build identity: a stable build
// is never offered a prerelease (even with MCPPROXY_ALLOW_PRERELEASE_UPDATES
// set), an RC build tracks the rc channel, and a dev build honors the env flag.
func TestApp_IncludePrereleases_BuildIdentity(t *testing.T) {
	t.Run("stable build ignores env flag", func(t *testing.T) {
		t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "true")
		a := &App{version: "v1.2.3"}
		if a.includePrereleases() {
			t.Error("stable build must never include prereleases, even with the env override")
		}
	})

	t.Run("rc build includes prereleases", func(t *testing.T) {
		a := &App{version: "v1.3.0-rc.1"}
		if !a.includePrereleases() {
			t.Error("rc build must track the rc channel")
		}
	})

	t.Run("dev build honors env flag", func(t *testing.T) {
		t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "true")
		a := &App{version: "dev"}
		if !a.includePrereleases() {
			t.Error("dev build must honor the env override")
		}
	})

	t.Run("dev build defaults to stable", func(t *testing.T) {
		t.Setenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES", "")
		a := &App{version: "dev"}
		if a.includePrereleases() {
			t.Error("dev build without opt-in must not include prereleases")
		}
	})
}
