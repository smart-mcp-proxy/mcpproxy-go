package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// resolveCase is one cell of the global × per-server × structural matrix.
type resolveCase struct {
	name        string
	global      *config.DockerIsolationConfig
	server      *config.ServerConfig
	wantMode    config.IsolationMode
	wantIso     bool
	wantSource  string
	wantInherit bool
}

func stdioServer() *config.ServerConfig {
	return &config.ServerConfig{Name: "srv", Command: "npx", Args: []string{"some-mcp"}}
}

func resolveCases() []resolveCase {
	withIso := func(iso *config.IsolationConfig) *config.ServerConfig {
		s := stdioServer()
		s.Isolation = iso
		return s
	}
	sandbox := config.IsolationModeSandbox

	return []resolveCase{
		{
			// GH #1142: this is the reported bug. No per-server override at
			// all, global isolation on ⇒ the server IS isolated and the
			// resolution is inherited.
			name:        "inherit global docker",
			global:      &config.DockerIsolationConfig{Enabled: true},
			server:      withIso(&config.IsolationConfig{Image: "python:3.12"}),
			wantMode:    config.IsolationModeDocker,
			wantIso:     true,
			wantSource:  config.IsolationSourceGlobal,
			wantInherit: true,
		},
		{
			name:        "no isolation block at all inherits global docker",
			global:      &config.DockerIsolationConfig{Enabled: true},
			server:      stdioServer(),
			wantMode:    config.IsolationModeDocker,
			wantIso:     true,
			wantSource:  config.IsolationSourceGlobal,
			wantInherit: true,
		},
		{
			name:        "explicit per-server opt-out under global docker",
			global:      &config.DockerIsolationConfig{Enabled: true},
			server:      withIso(&config.IsolationConfig{Enabled: config.BoolPtr(false)}),
			wantMode:    config.IsolationModeNone,
			wantIso:     false,
			wantSource:  config.IsolationSourceServerOptOut,
			wantInherit: false,
		},
		{
			name:        "explicit per-server opt-in under global docker",
			global:      &config.DockerIsolationConfig{Enabled: true},
			server:      withIso(&config.IsolationConfig{Enabled: config.BoolPtr(true)}),
			wantMode:    config.IsolationModeDocker,
			wantIso:     true,
			wantSource:  config.IsolationSourceGlobal,
			wantInherit: false,
		},
		{
			name:        "explicit per-server opt-in ignored when global is off",
			global:      &config.DockerIsolationConfig{Enabled: false},
			server:      withIso(&config.IsolationConfig{Enabled: config.BoolPtr(true)}),
			wantMode:    config.IsolationModeNone,
			wantIso:     false,
			wantSource:  config.IsolationSourceServerOptInIgnored,
			wantInherit: false,
		},
		{
			name:        "global off, no override",
			global:      &config.DockerIsolationConfig{Enabled: false},
			server:      stdioServer(),
			wantMode:    config.IsolationModeNone,
			wantIso:     false,
			wantSource:  config.IsolationSourceGlobal,
			wantInherit: true,
		},
		{
			name:        "per-server mode override wins over global docker",
			global:      &config.DockerIsolationConfig{Enabled: true},
			server:      withIso(&config.IsolationConfig{Mode: &sandbox}),
			wantMode:    config.IsolationModeSandbox,
			wantIso:     true,
			wantSource:  config.IsolationSourceServerMode,
			wantInherit: false,
		},
		{
			name:        "per-server mode override wins over a disabled global",
			global:      &config.DockerIsolationConfig{Enabled: false},
			server:      withIso(&config.IsolationConfig{Mode: &sandbox}),
			wantMode:    config.IsolationModeSandbox,
			wantIso:     true,
			wantSource:  config.IsolationSourceServerMode,
			wantInherit: false,
		},
		{
			name:   "http server is never isolated",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: &config.ServerConfig{
				Name: "remote", URL: "https://example.com/mcp", Protocol: "http",
			},
			wantMode:    config.IsolationModeNone,
			wantIso:     false,
			wantSource:  config.IsolationSourceNotStdio,
			wantInherit: true,
		},
		{
			name:   "docker-command server is never re-wrapped",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: &config.ServerConfig{
				Name: "dockerised", Command: "docker", Args: []string{"run", "img"},
			},
			wantMode:    config.IsolationModeNone,
			wantIso:     false,
			wantSource:  config.IsolationSourceAlreadyDocker,
			wantInherit: true,
		},
	}
}

// TestResolveIsolation_Source pins the effective mode, the isolated flag, and
// the explanation string for every cell of the resolution matrix. The Source
// string is what makes "inherit" a distinguishable state instead of a guess,
// so it is asserted exactly (GH #1142).
func TestResolveIsolation_Source(t *testing.T) {
	for _, tc := range resolveCases() {
		t.Run(tc.name, func(t *testing.T) {
			im := NewIsolationManager(tc.global)
			got := im.ResolveIsolation(tc.server)

			if got.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if got.Isolated != tc.wantIso {
				t.Errorf("Isolated = %v, want %v", got.Isolated, tc.wantIso)
			}
			if got.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.Inherited != tc.wantInherit {
				t.Errorf("Inherited = %v, want %v", got.Inherited, tc.wantInherit)
			}
			if want := tc.global.ResolvedMode(); got.GlobalMode != want {
				t.Errorf("GlobalMode = %q, want %q", got.GlobalMode, want)
			}
		})
	}
}

// TestResolveIsolation_NilServer guards the nil path taken by callers that
// project a server list before the config is attached.
func TestResolveIsolation_NilServer(t *testing.T) {
	im := NewIsolationManager(&config.DockerIsolationConfig{Enabled: true})
	got := im.ResolveIsolation(nil)
	if got.Mode != config.IsolationModeNone || got.Isolated {
		t.Errorf("nil server should resolve to none/unisolated, got %+v", got)
	}
}

// TestResolveModeDelegatesToResolveIsolation pins the single-algorithm
// invariant: ResolveMode and ShouldIsolate must never drift from
// ResolveIsolation, because the spawn path and the reporting path both have to
// answer "is this server isolated" the same way (GH #1142).
func TestResolveModeDelegatesToResolveIsolation(t *testing.T) {
	for _, tc := range resolveCases() {
		t.Run(tc.name, func(t *testing.T) {
			im := NewIsolationManager(tc.global)
			resolved := im.ResolveIsolation(tc.server)

			if got := im.ResolveMode(tc.server); got != resolved.Mode {
				t.Errorf("ResolveMode() = %q, ResolveIsolation().Mode = %q", got, resolved.Mode)
			}
			wantShould := resolved.Mode == config.IsolationModeDocker
			if got := im.ShouldIsolate(tc.server); got != wantShould {
				t.Errorf("ShouldIsolate() = %v, want %v", got, wantShould)
			}
		})
	}
}
