package config

import (
	"fmt"
	"strconv"
	"strings"
)

// MsgRequireMCPAuthOverridden is the boot notice and doctor finding text of
// contracts/config-keys.md for an explicit require_mcp_auth: false under an
// enabled server-edition block (Spec 107 FR-029).
const MsgRequireMCPAuthOverridden = "require_mcp_auth: false is overridden to true because server_edition.enabled is true"

// DoctorFindings returns the configuration findings `mcpproxy doctor` renders
// as runtime_warnings (Spec 107 T052). It reads the config it is handed — the
// caller passes the LIVE snapshot — and never mutates it. Edition-neutral
// findings live here; the server-only ones come from the build-tagged
// serverEditionDoctorFindings (a no-op on the personal build).
func DoctorFindings(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	if f := StdioSocketExposureFinding(cfg); f != "" {
		out = append(out, f)
	}
	if f := ScanDefaultTrustFinding(cfg); f != "" {
		out = append(out, f)
	}
	return append(out, serverEditionDoctorFindings(cfg)...)
}

// ScanDefaultTrustServers lists, in config order, the enabled servers whose
// trust mode is the scan DEFAULT by omission (TrustModeDefaulted): no
// trust_mode, no legacy flag.
func ScanDefaultTrustServers(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	for _, s := range cfg.Servers {
		if s != nil && s.Enabled && s.TrustModeDefaulted() {
			names = append(names, s.Name)
		}
	}
	return names
}

// ScanDefaultTrustFinding is the spec 120 FR-012 doctor finding: every enabled
// server with no trust_mode runs under scan (the default since spec 119), so a
// changed tool contract that scans clean and passes the structural gate is
// approved without a person. Returns "" when no server is defaulted.
func ScanDefaultTrustFinding(cfg *Config) string {
	names := ScanDefaultTrustServers(cfg)
	if len(names) == 0 {
		return ""
	}
	return ScanDefaultTrustMessage(names)
}

// ScanDefaultTrustMessage renders the scan-default notice for the named
// servers; the startup WARN (spec 120 FR-011) and doctor share it.
func ScanDefaultTrustMessage(names []string) string {
	noun, verb := "server has", "runs"
	if len(names) != 1 {
		noun, verb = "servers have", "run"
	}
	return fmt.Sprintf("%d enabled %s no trust_mode and %s under scan, the default: %s. "+
		"A changed tool contract that scans clean and passes the structural gate is approved automatically, "+
		"and held changes already in the review queue are released on the next discovery pass. "+
		"Set \"trust_mode\": \"manual\" on a server whose tool changes a person should review.",
		len(names), noun, verb, strings.Join(names, ", "))
}

// StdioSocketExposureFinding is the GH #1704 doctor finding: every enabled
// stdio server whose resolved isolation mode is none or sandbox while
// enable_socket is on, in config order. Such a child runs as the proxy's user,
// so it can read the data directory (mcp_config.json with api_key, config.db
// with OAuth tokens) and connect to the admin socket in that directory, which
// trusts every connection without a key — a sandboxed child minted an agent
// token over it. Landlock confines writes only; neither mode hides either.
// Servers resolved to docker, servers that run docker themselves and servers
// with no child process are not listed. Returns "" when there is nothing to
// report.
func StdioSocketExposureFinding(cfg *Config) string {
	if cfg == nil || !cfg.EnableSocket {
		return ""
	}
	var exposed []string
	for _, s := range cfg.Servers {
		if s == nil || !s.Enabled || s.Command == "" || IsDockerCommand(s.Command) {
			// No child process, or the server already runs in a container of its
			// own (the structural gate only fires for non-none modes, so check it
			// here too).
			continue
		}
		if ResolveIsolation(cfg.DockerIsolation, s).Mode == IsolationModeDocker {
			continue
		}
		exposed = append(exposed, strconv.Quote(s.Name))
	}
	if len(exposed) == 0 {
		return ""
	}
	return fmt.Sprintf("%d enabled stdio server(s) run under isolation none or sandbox while enable_socket is true (%s): "+
		"a stdio server runs as the proxy's user, can read the data directory (mcp_config.json holds api_key, config.db holds OAuth tokens) "+
		"and can mint agent tokens over the admin socket in that directory without a key; the sandbox mode confines writes only. "+
		"Set enable_socket: false on headless installs and use docker isolation (docker_isolation.mode: \"docker\", or per-server isolation.mode) for servers you do not trust. "+
		"See https://docs.mcpproxy.app/features/sandbox-isolation/",
		len(exposed), strings.Join(exposed, ", "))
}
