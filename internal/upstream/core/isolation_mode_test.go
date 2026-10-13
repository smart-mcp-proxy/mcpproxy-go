package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func modePtr(m config.IsolationMode) *config.IsolationMode { return &m }

// TestIsolationManager_ResolveMode covers the mode-aware selector (MCP-34.2):
// global back-compat (Enabled bool ⇒ docker/none), per-server bool opt-out,
// per-server explicit mode override, and the structural gates (HTTP servers
// and docker-command servers are never isolated regardless of mode).
func TestIsolationManager_ResolveMode(t *testing.T) {
	stdio := func() *config.ServerConfig {
		return &config.ServerConfig{Name: "srv", Command: "npx", Args: []string{"some-mcp"}}
	}

	tests := []struct {
		name   string
		global *config.DockerIsolationConfig
		server *config.ServerConfig
		want   config.IsolationMode
	}{
		{
			name:   "global off ⇒ none",
			global: &config.DockerIsolationConfig{Enabled: false},
			server: stdio(),
			want:   config.IsolationModeNone,
		},
		{
			name:   "legacy global enabled ⇒ docker",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: stdio(),
			want:   config.IsolationModeDocker,
		},
		{
			name:   "global sandbox mode ⇒ sandbox",
			global: &config.DockerIsolationConfig{Mode: config.IsolationModeSandbox},
			server: stdio(),
			want:   config.IsolationModeSandbox,
		},
		{
			name:   "per-server bool opt-out under global docker ⇒ none",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig {
				s := stdio()
				s.Isolation = &config.IsolationConfig{Enabled: config.BoolPtr(false)}
				return s
			}(),
			want: config.IsolationModeNone,
		},
		{
			name:   "per-server bool opt-in under global docker ⇒ docker",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig {
				s := stdio()
				s.Isolation = &config.IsolationConfig{Enabled: config.BoolPtr(true)}
				return s
			}(),
			want: config.IsolationModeDocker,
		},
		{
			name:   "per-server explicit mode overrides global docker ⇒ sandbox",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig {
				s := stdio()
				s.Isolation = &config.IsolationConfig{Mode: modePtr(config.IsolationModeSandbox)}
				return s
			}(),
			want: config.IsolationModeSandbox,
		},
		{
			name:   "per-server explicit mode:none overrides global docker ⇒ none",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig {
				s := stdio()
				s.Isolation = &config.IsolationConfig{Mode: modePtr(config.IsolationModeNone)}
				return s
			}(),
			want: config.IsolationModeNone,
		},
		{
			name:   "per-server explicit mode wins even when global is off ⇒ sandbox",
			global: &config.DockerIsolationConfig{Enabled: false},
			server: func() *config.ServerConfig {
				s := stdio()
				s.Isolation = &config.IsolationConfig{Mode: modePtr(config.IsolationModeSandbox)}
				return s
			}(),
			want: config.IsolationModeSandbox,
		},
		{
			name:   "http server (no command) ⇒ none even under global docker",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig { s := stdio(); s.Command = ""; return s }(),
			want:   config.IsolationModeNone,
		},
		{
			name:   "docker-command server is never isolated",
			global: &config.DockerIsolationConfig{Enabled: true},
			server: func() *config.ServerConfig { s := stdio(); s.Command = "docker"; return s }(),
			want:   config.IsolationModeNone,
		},
		{
			name:   "docker-command server is never sandboxed either",
			global: &config.DockerIsolationConfig{Mode: config.IsolationModeSandbox},
			server: func() *config.ServerConfig { s := stdio(); s.Command = "docker"; return s }(),
			want:   config.IsolationModeNone,
		},
		{
			name:   "nil global config ⇒ none",
			global: nil,
			server: stdio(),
			want:   config.IsolationModeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			im := NewIsolationManager(tt.global)
			if got := im.ResolveMode(tt.server); got != tt.want {
				t.Errorf("ResolveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsolationManager_ShouldIsolate_ConsistentWithResolveMode verifies the
// legacy bool wrapper stays in lockstep with the new resolver: ShouldIsolate
// is true iff the resolved mode is docker.
func TestIsolationManager_ShouldIsolate_ConsistentWithResolveMode(t *testing.T) {
	cases := []*config.DockerIsolationConfig{
		{Enabled: false},
		{Enabled: true},
		{Mode: config.IsolationModeSandbox},
		{Mode: config.IsolationModeDocker},
		{Mode: config.IsolationModeNone},
	}
	server := &config.ServerConfig{Name: "srv", Command: "npx", Args: []string{"some-mcp"}}
	for _, g := range cases {
		im := NewIsolationManager(g)
		want := im.ResolveMode(server) == config.IsolationModeDocker
		if got := im.ShouldIsolate(server); got != want {
			t.Errorf("global=%+v: ShouldIsolate()=%v, want %v (ResolveMode=%q)", g, got, want, im.ResolveMode(server))
		}
	}
}
