package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// sandboxEnforceable reports whether the "sandbox" isolation mode can actually
// confine a child process on THIS host. It is the same predicate the spawn path
// degrades on: `core.Client.wrapWithSandbox` returns the command UNCHANGED off
// Linux, and warns that confinement will not take effect on a Linux kernel
// without Landlock. Reporting isolated:true in either case would be a false
// isolation claim, which is worse than no claim at all.
//
// `sandbox.Available()` performs a syscall, so the answer is memoized — the
// resolver runs on every tool dispatch. It is a var so tests can assert both
// halves of the matrix on any platform.
var sandboxEnforceable = sync.OnceValue(sandbox.Available)

// SandboxEnforceable reports whether the "sandbox" isolation mode can actually
// confine a child process on this host. Exported so surfaces that explain
// isolation state (and tests that assert the mode matrix) can ask the same
// question the resolver asks, instead of re-deriving it from GOOS.
func SandboxEnforceable() bool { return sandboxEnforceable() }

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
	// IsolationSourceSandboxUnavailable: capability gate — mode is "sandbox"
	// but this host cannot enforce it (any non-Linux OS, or a Linux kernel
	// without Landlock), so the child process runs unconfined.
	IsolationSourceSandboxUnavailable = "sandbox-unavailable"
	// IsolationSourceUnsupportedMode: the configured mode is not one the spawn
	// path implements, so nothing wraps the child process. Reachable from a
	// hand-edited config file; the write surfaces reject unknown modes.
	IsolationSourceUnsupportedMode = "unsupported-mode"
)

// ValidateIsolationModeOverride reports whether a per-server `isolation.mode`
// override is a mode the spawn path actually implements.
//
// Both write seams (REST `mode_override`, MCP `isolation_json`) call this so an
// unknown value is refused where the operator can see it, instead of being
// persisted to BBolt and the config file and only surfacing as a failed
// validation on the NEXT daemon start.
//
// nil and the empty string are "unset" (inherit the global mode), not errors.
func ValidateIsolationModeOverride(mode *IsolationMode) error {
	if mode == nil || *mode == "" {
		return nil
	}
	if !mode.IsValid() {
		return fmt.Errorf("invalid isolation mode %q: must be one of: %s, %s, %s "+
			"(values are case-sensitive; send an empty string to clear the override and inherit the global setting)",
			*mode, IsolationModeDocker, IsolationModeSandbox, IsolationModeNone)
	}
	return nil
}

// ResolvedIsolation is the fully-resolved isolation state of a server: what
// actually happens at spawn time, plus enough context for a UI to explain it.
//
// This is the ONLY correct answer to "is this server isolated". The raw
// per-server override (IsolationConfig.Enabled) is tri-state and means nothing
// on its own — nil is "inherit", not "off" (GH #1142).
type ResolvedIsolation struct {
	// Mode is the effective isolation mode: docker | sandbox | none. This is
	// exactly what the spawn path branches on, so it must never be adjusted for
	// host capability — on a Linux kernel without Landlock the sandbox wrapper
	// still runs and still applies its rlimits.
	Mode IsolationMode
	// Isolated reports whether the child process is actually CONFINED — i.e.
	// whether Mode is one the spawn path implements AND this host can enforce
	// it. It is not simply `Mode != none`: "sandbox" on macOS/Windows, on a
	// kernel without Landlock, or a mode the launcher does not implement, all
	// run the server unconfined, and the read path must never claim isolation
	// the spawn path will not deliver (GH #1142). Source says which.
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
//
// A final CAPABILITY gate decides Isolated (not Mode): a mode the launcher does
// not implement, and "sandbox" on a host that cannot enforce Landlock, both run
// the server unconfined, so they must not be reported as isolated. Mode is left
// alone there because the spawn path branches on it and still applies the
// sandbox wrapper's rlimits on Linux — see isolationDelivered.
func ResolveIsolation(globalConfig *DockerIsolationConfig, serverConfig *ServerConfig) ResolvedIsolation {
	globalMode := globalConfig.ResolvedMode() // nil-safe; returns none for nil

	var iso *IsolationConfig
	if serverConfig != nil {
		iso = serverConfig.Isolation
	}
	inherited := !iso.HasEnabledOverride() && !iso.HasModeOverride()

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

	// Capability gate: does the spawn path actually confine this mode here?
	isolated, source := isolationDelivered(mode, source)

	return ResolvedIsolation{
		Mode:       mode,
		Isolated:   isolated,
		GlobalMode: globalMode,
		Inherited:  inherited,
		Source:     source,
	}
}

// isolationDelivered answers "will the child process really be confined" for an
// already-resolved mode, and refines the source when the answer is no for a
// reason the mode alone does not explain.
//
// This is the read-path half of the single-algorithm invariant: the spawn path
// branches on Mode (`connection_stdio.go` / `connection_launcher.go` match
// "docker" via ShouldIsolate and "sandbox" explicitly), and every OTHER value
// falls through both branches and launches unwrapped. Anything this function
// calls unisolated is exactly what those branches decline to wrap or what
// wrapWithSandbox degrades to unconfined.
func isolationDelivered(mode IsolationMode, source string) (isolated bool, refinedSource string) {
	switch mode {
	case IsolationModeNone:
		return false, source
	case IsolationModeDocker:
		return true, source
	case IsolationModeSandbox:
		if !sandboxEnforceable() {
			return false, IsolationSourceSandboxUnavailable
		}
		return true, source
	default:
		// Unset or unrecognized: the launcher implements no such mode, so the
		// server runs unconfined however the value got into the config.
		return false, IsolationSourceUnsupportedMode
	}
}

// resolveConfiguredIsolationMode applies the global + per-server config
// precedence, before the structural gates in ResolveIsolation.
func resolveConfiguredIsolationMode(iso *IsolationConfig, globalMode IsolationMode) (IsolationMode, string) {
	// (1) A per-server explicit Mode override wins outright. A pointer to the
	// empty string is "unset" (the documented meaning of an empty mode), not an
	// override, so it falls through to inherit rather than resolving to a mode
	// nothing implements.
	if iso.HasModeOverride() {
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
