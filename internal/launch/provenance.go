// Package launch exposes the core process's durable launch provenance: which
// component started this mcpproxy core (Spec 092 FR-001a).
//
// Why a dedicated package rather than reusing internal/telemetry's
// LaunchSource: telemetry classifies for analytics and falls back to PPID/TTY
// heuristics (login_item, cli, unknown). FR-001a needs the *asserted* marker
// only — "a tray spawned me" / "the installer spawned me" — because the tray
// uses it to decide whether it may stop and respawn a core it did not itself
// start. A heuristic guess must never authorize killing someone else's
// process, so anything that is not an explicit marker reports "" (user or
// unknown provenance) and the tray must ask for consent instead (FR-002).
package launch

import (
	"os"
	"strings"
	"sync"
)

// EnvLaunchedBy is the environment variable both trays and the macOS
// installer stamp on the core they spawn:
//   - native/macos/.../CoreProcessManager.swift  → tray
//   - cmd/mcpproxy-tray/main.go                  → tray
//   - packaging/macos/postinstall.sh             → installer
const EnvLaunchedBy = "MCPPROXY_LAUNCHED_BY"

// Canonical provenance values. Anything else — including an unset,
// misspelled, or attacker-supplied value — normalizes to ByUnknown.
const (
	// ByTray: a tray process spawned this core and owns its lifecycle, so a
	// newer tray may supersede it without asking (FR-001).
	ByTray = "tray"

	// ByInstaller: the macOS PKG postinstall launched the app that spawned
	// this core.
	ByInstaller = "installer"

	// ByUnknown: user-launched (terminal, launchd unit, brew services, …) or
	// unknowable. Serialized as the empty string so API consumers can treat
	// "absent" and "not tray-owned" identically.
	ByUnknown = ""
)

// Classify normalizes a raw MCPPROXY_LAUNCHED_BY value. It is deliberately
// strict: only the two canonical markers are honored (case-insensitively,
// with surrounding whitespace trimmed, because `open --env` and shell
// wrappers are easy to get slightly wrong), everything else is ByUnknown.
func Classify(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ByTray:
		return ByTray
	case ByInstaller:
		return ByInstaller
	default:
		return ByUnknown
	}
}

var (
	captureOnce sync.Once
	captured    string
)

// LaunchedBy returns this process's launch provenance, captured from the
// environment exactly once. Capturing once matters: the value must describe
// how the process was *started*, and later os.Setenv calls (config reload,
// child-process env shaping) must not be able to rewrite history.
func LaunchedBy() string {
	captureOnce.Do(func() {
		captured = Classify(os.Getenv(EnvLaunchedBy))
	})
	return captured
}
