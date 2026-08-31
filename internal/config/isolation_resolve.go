package config

import (
	"path/filepath"
	"strings"
)

// Isolation resolution sources. These are a stable vocabulary: they are
// serialized onto the REST/MCP surfaces so a UI can explain WHY a server is (or
// is not) isolated instead of rendering a bare toggle. Clients must treat an
// unrecognized value as "inherited from the global setting".
const (
	// IsolationSourceGlobal: the effective mode came from the global config —
	// either because the server sets no override, or because its explicit
	// enabled:true simply agrees with the global mode.
	IsolationSourceGlobal = "global"
	// IsolationSourceServerMode: a per-server `isolation.mode` override won.
	IsolationSourceServerMode = "server-mode"
	// IsolationSourceServerOptOut: a per-server `isolation.enabled:false`
	// downgraded an otherwise-active global mode.
	IsolationSourceServerOptOut = "server-opt-out"
	// IsolationSourceServerOptInIgnored: the server sets `isolation.enabled:true`
	// but global isolation is off, so the opt-in is ignored.
	IsolationSourceServerOptInIgnored = "server-opt-in-ignored"
	// IsolationSourceNotStdio: structural gate — the server has no local
	// command, so there is no child process to isolate.
	IsolationSourceNotStdio = "not-stdio"
	// IsolationSourceAlreadyDocker: structural gate — the server already
	// invokes docker itself, so wrapping it would break its socket access.
	IsolationSourceAlreadyDocker = "already-docker"
)

// ResolvedIsolation is the fully-resolved isolation state of a server: what
// actually happens at spawn time, plus enough context for a UI to explain it.
//
// This is the ONLY correct answer to "is this server isolated". The raw
// per-server override (IsolationConfig.Enabled) is tri-state and means nothing
// on its own — nil is "inherit", not "off" (GH #1142).
type ResolvedIsolation struct {
	// Mode is the effective isolation mode: docker | sandbox | none.
	Mode IsolationMode
	// Isolated is Mode != none.
	Isolated bool
	// GlobalMode is what "inherit" resolves to right now.
	GlobalMode IsolationMode
	// Inherited is true when the server carries no per-server enabled/mode
	// override, so its state tracks the global setting.
	Inherited bool
	// Source explains which rule produced Mode; one of the IsolationSource*
	// constants above.
	Source string
}

// ResolveIsolation resolves the effective isolation state for a server,
// combining the global config (with legacy Enabled⇒docker back-compat), the
// optional per-server override, and the structural gates — and reports which
// rule decided the outcome.
//
// It lives in `config` rather than `upstream/core` so that every surface which
// has to answer "is this server isolated" — the spawn path, the REST/MCP
// projections, and the contracts converter (which cannot import core without a
// cycle) — provably runs one algorithm. `core.IsolationManager.ResolveIsolation`
// wraps this to add the deduplicated ignored-opt-in warning.
//
// Precedence:
//  1. A per-server explicit Mode wins outright (even over a disabled global) —
//     mirroring how other per-server overrides (image, network) take priority.
//  2. Otherwise, when the global mode resolves to none, per-server bool opt-ins
//     are ignored, preserving the pre-mode behavior.
//  3. When the global mode is active, a per-server bool opt-out (enabled:false)
//     downgrades the server to none. A nil Enabled is "inherit", NOT an opt-out.
//
// Structural gates then apply to ALL non-none modes: servers with no command
// (HTTP transports) and servers that already invoke docker are never isolated.
func ResolveIsolation(globalConfig *DockerIsolationConfig, serverConfig *ServerConfig) ResolvedIsolation {
	globalMode := globalConfig.ResolvedMode() // nil-safe; returns none for nil

	var iso *IsolationConfig
	if serverConfig != nil {
		iso = serverConfig.Isolation
	}
	inherited := !iso.HasEnabledOverride() && (iso == nil || iso.Mode == nil)

	mode, source := resolveConfiguredIsolationMode(iso, globalMode)

	// Structural gates apply to ALL non-none modes.
	if mode != IsolationModeNone {
		switch {
		case serverConfig == nil || serverConfig.Command == "":
			mode, source = IsolationModeNone, IsolationSourceNotStdio
		case IsDockerCommand(serverConfig.Command):
			mode, source = IsolationModeNone, IsolationSourceAlreadyDocker
		}
	}

	return ResolvedIsolation{
		Mode:       mode,
		Isolated:   mode != IsolationModeNone,
		GlobalMode: globalMode,
		Inherited:  inherited,
		Source:     source,
	}
}

// resolveConfiguredIsolationMode applies the global + per-server config
// precedence, before the structural gates in ResolveIsolation.
func resolveConfiguredIsolationMode(iso *IsolationConfig, globalMode IsolationMode) (IsolationMode, string) {
	// (1) A per-server explicit Mode override wins outright.
	if iso != nil && iso.Mode != nil {
		return *iso.Mode, IsolationSourceServerMode
	}

	// (2) Global isolation off: per-server bool opt-ins are ignored.
	if globalMode == IsolationModeNone {
		if iso.IsExplicitlyEnabled() {
			return IsolationModeNone, IsolationSourceServerOptInIgnored
		}
		return IsolationModeNone, IsolationSourceGlobal
	}

	// (3) Global isolation active: honor a per-server bool opt-out.
	if iso.IsExplicitlyDisabled() {
		return IsolationModeNone, IsolationSourceServerOptOut
	}

	return globalMode, IsolationSourceGlobal
}

// IsDockerCommand reports whether a server's command already invokes Docker.
// Such servers are typically pre-configured containers; wrapping them (in a
// container or a Landlock sandbox) would break their access to the Docker socket.
func IsDockerCommand(command string) bool {
	return filepath.Base(command) == "docker" || strings.Contains(command, "docker")
}
