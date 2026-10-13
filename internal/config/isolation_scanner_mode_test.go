package config

import "testing"

// isoModePtr builds a per-server isolation.mode override.
func isoModePtr(m IsolationMode) *IsolationMode { return &m }

// scannerModeCase is one row of the paired matrix below. Every row asserts BOTH
// questions at once so the two can never silently converge again (GH #1303):
//
//   - wantSpawnMode/wantSpawnSource: ResolveIsolation — "do we containerise or
//     confine THIS server's own child process";
//   - wantScanMode/wantScanSource: ResolveScannerIsolationMode — "may the
//     scanner engine run its own short-lived Docker containers against this
//     server's already-captured tool definitions".
type scannerModeCase struct {
	name   string
	global *DockerIsolationConfig
	server *ServerConfig

	wantSpawnMode   IsolationMode
	wantSpawnSource string

	wantScanMode   IsolationMode
	wantScanSource string
}

// scannerModeCases is the shared matrix. The rows marked RED are the #1303
// regression: a remote (command-less) HTTP/SSE server whose scanner answer was
// being decided by ResolveIsolation's process-spawn structural gates.
func scannerModeCases() []scannerModeCase {
	return []scannerModeCase{
		{
			// RED before the fix: the scanner answer was none/not-stdio, so
			// every Docker scanner plugin was skipped for every remote server.
			name:            "RED: remote http server inherits a global docker mode",
			global:          &DockerIsolationConfig{Enabled: true},
			server:          &ServerConfig{Name: "remote", URL: "https://example.test/mcp", Protocol: "http"},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceNotStdio,
			wantScanMode:    IsolationModeDocker,
			wantScanSource:  IsolationSourceGlobal,
		},
		{
			// RED before the fix: the remediation the scanner's own skip message
			// prints ("set isolation.mode to \"docker\" for this server") was a
			// dead end for command-less servers.
			name:   "RED: remote http server pinned to isolation.mode:docker",
			global: &DockerIsolationConfig{Mode: IsolationModeNone},
			server: &ServerConfig{
				Name: "remote", URL: "https://example.test/mcp", Protocol: "http",
				Isolation: &IsolationConfig{Mode: isoModePtr(IsolationModeDocker)},
			},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceNotStdio,
			wantScanMode:    IsolationModeDocker,
			wantScanSource:  IsolationSourceServerMode,
		},
		{
			// RED before the fix: the already-docker structural gate also exists
			// only to avoid double-wrapping a self-dockerising CHILD PROCESS; it
			// says nothing about separate scanner containers.
			name:            "RED: self-dockerising stdio server under a global docker mode",
			global:          &DockerIsolationConfig{Enabled: true},
			server:          &ServerConfig{Name: "selfdocker", Command: "docker", Args: []string{"run", "-i", "img"}, Protocol: "stdio"},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceAlreadyDocker,
			wantScanMode:    IsolationModeDocker,
			wantScanSource:  IsolationSourceGlobal,
		},
		{
			// (1) explicit per-server security policy: mode:none must STILL keep
			// Docker scanners off for this server.
			name:   "policy: remote http server pinned to isolation.mode:none",
			global: &DockerIsolationConfig{Enabled: true},
			server: &ServerConfig{
				Name: "remote", URL: "https://example.test/mcp", Protocol: "http",
				Isolation: &IsolationConfig{Mode: isoModePtr(IsolationModeNone)},
			},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceServerMode,
			wantScanMode:    IsolationModeNone,
			wantScanSource:  IsolationSourceServerMode,
		},
		{
			// (1) the bool opt-out shape of the same policy.
			name:   "policy: remote http server with isolation.enabled:false",
			global: &DockerIsolationConfig{Enabled: true},
			server: &ServerConfig{
				Name: "remote", URL: "https://example.test/mcp", Protocol: "http",
				Isolation: &IsolationConfig{Enabled: BoolPtr(false)},
			},
			// The opt-out resolves to none before the structural gate can fire
			// (the gates only downgrade a non-none mode), so both paths already
			// agree on the source here.
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceServerOptOut,
			wantScanMode:    IsolationModeNone,
			wantScanSource:  IsolationSourceServerOptOut,
		},
		{
			// (2) a global sandbox mode still blocks Docker scanners: the host
			// runs no Docker for them.
			name:            "policy: remote http server under a global sandbox mode",
			global:          &DockerIsolationConfig{Mode: IsolationModeSandbox},
			server:          &ServerConfig{Name: "remote", URL: "https://example.test/mcp", Protocol: "http"},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceNotStdio,
			wantScanMode:    IsolationModeSandbox,
			wantScanSource:  IsolationSourceGlobal,
		},
		{
			// (2) the per-server shape of the same.
			name:   "policy: remote http server pinned to isolation.mode:sandbox",
			global: &DockerIsolationConfig{Enabled: true},
			server: &ServerConfig{
				Name: "remote", URL: "https://example.test/mcp", Protocol: "http",
				Isolation: &IsolationConfig{Mode: isoModePtr(IsolationModeSandbox)},
			},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceNotStdio,
			wantScanMode:    IsolationModeSandbox,
			wantScanSource:  IsolationSourceServerMode,
		},
		{
			// Global off: a per-server bool opt-in stays ignored on BOTH paths.
			name:   "policy: remote http server opts in while global is off",
			global: &DockerIsolationConfig{Mode: IsolationModeNone},
			server: &ServerConfig{
				Name: "remote", URL: "https://example.test/mcp", Protocol: "http",
				Isolation: &IsolationConfig{Enabled: BoolPtr(true)},
			},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceServerOptInIgnored,
			wantScanMode:    IsolationModeNone,
			wantScanSource:  IsolationSourceServerOptInIgnored,
		},
		{
			// Global off, no override: nothing to run scanners in.
			name:            "policy: remote http server with global isolation off",
			global:          &DockerIsolationConfig{Mode: IsolationModeNone},
			server:          &ServerConfig{Name: "remote", URL: "https://example.test/mcp", Protocol: "http"},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceGlobal,
			wantScanMode:    IsolationModeNone,
			wantScanSource:  IsolationSourceGlobal,
		},
		{
			// An ordinary stdio server: the two questions already agreed, and
			// must keep agreeing.
			name:            "unchanged: plain stdio server under a global docker mode",
			global:          &DockerIsolationConfig{Enabled: true},
			server:          &ServerConfig{Name: "npx-srv", Command: "npx", Protocol: "stdio"},
			wantSpawnMode:   IsolationModeDocker,
			wantSpawnSource: IsolationSourceGlobal,
			wantScanMode:    IsolationModeDocker,
			wantScanSource:  IsolationSourceGlobal,
		},
		{
			name:            "unchanged: plain stdio server with global isolation off",
			global:          &DockerIsolationConfig{Mode: IsolationModeNone},
			server:          &ServerConfig{Name: "npx-srv", Command: "npx", Protocol: "stdio"},
			wantSpawnMode:   IsolationModeNone,
			wantSpawnSource: IsolationSourceGlobal,
			wantScanMode:    IsolationModeNone,
			wantScanSource:  IsolationSourceGlobal,
		},
	}
}

// TestResolveScannerIsolationMode_PairedWithResolveIsolation pins both isolation
// questions against each other on every row, so a future edit that makes one
// answer the other is visible in a single file (GH #1303).
func TestResolveScannerIsolationMode_PairedWithResolveIsolation(t *testing.T) {
	// The sandbox rows must not depend on the host: Mode is capability-blind by
	// design, but ResolveIsolation refines Source to sandbox-unavailable.
	withSandboxEnforceable(t, true)

	for _, tc := range scannerModeCases() {
		t.Run(tc.name, func(t *testing.T) {
			spawn := ResolveIsolation(tc.global, tc.server)
			if spawn.Mode != tc.wantSpawnMode {
				t.Errorf("ResolveIsolation().Mode = %q, want %q", spawn.Mode, tc.wantSpawnMode)
			}
			if spawn.Source != tc.wantSpawnSource {
				t.Errorf("ResolveIsolation().Source = %q, want %q", spawn.Source, tc.wantSpawnSource)
			}

			scanMode, scanSource := ResolveScannerIsolationMode(tc.global, tc.server)
			if scanMode != tc.wantScanMode {
				t.Errorf("ResolveScannerIsolationMode() mode = %q, want %q", scanMode, tc.wantScanMode)
			}
			if scanSource != tc.wantScanSource {
				t.Errorf("ResolveScannerIsolationMode() source = %q, want %q", scanSource, tc.wantScanSource)
			}
		})
	}
}

// TestResolveScannerIsolationMode_MatchesStdioTwin is the anti-drift fence. For
// every row, an otherwise-identical server that DOES have a plain (non-docker)
// local command trips no structural gate, so its process-spawn mode is exactly
// the configured mode. The scanner answer must equal it — which is another way
// of saying the scanner resolver shares the precedence logic instead of
// reimplementing it.
//
// Only Mode is compared: ResolveIsolation additionally refines Source through
// the host-capability gate (sandbox-unavailable), which is a spawn-only concern.
func TestResolveScannerIsolationMode_MatchesStdioTwin(t *testing.T) {
	withSandboxEnforceable(t, true)

	for _, tc := range scannerModeCases() {
		t.Run(tc.name, func(t *testing.T) {
			twin := *tc.server
			twin.Command = "/usr/bin/some-stdio-server"
			twin.URL = ""
			twin.Protocol = "stdio"

			want := ResolveIsolation(tc.global, &twin).Mode
			got, _ := ResolveScannerIsolationMode(tc.global, tc.server)
			if got != want {
				t.Errorf("scanner mode = %q, but the stdio twin's spawn mode = %q: the two resolvers have drifted", got, want)
			}
		})
	}
}

// TestResolveScannerIsolationMode_NilServer documents the degenerate path: a nil
// server config carries no override, so it inherits the global mode and must not
// panic. The server-layer wiring returns "" for an unknown server before ever
// reaching here, and the scanner engine then applies the same global default.
func TestResolveScannerIsolationMode_NilServer(t *testing.T) {
	mode, source := ResolveScannerIsolationMode(&DockerIsolationConfig{Enabled: true}, nil)
	if mode != IsolationModeDocker {
		t.Errorf("mode = %q, want %q (nil server inherits the global mode)", mode, IsolationModeDocker)
	}
	if source != IsolationSourceGlobal {
		t.Errorf("source = %q, want %q", source, IsolationSourceGlobal)
	}

	if mode, _ := ResolveScannerIsolationMode(nil, nil); mode != IsolationModeNone {
		t.Errorf("nil global config: mode = %q, want %q", mode, IsolationModeNone)
	}
}
