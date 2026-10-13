package server

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestScannerIsolationModeForRemoteServer is the end-to-end regression for
// GH #1303: the isolation-mode resolver wired into the security scanner used to
// call the PROCESS-isolation resolver, whose "not-stdio" structural gate forces
// mode=none for every server without a local command. The scanner engine then
// skipped every Docker-based scanner plugin (mcp-ai-scanner, nova-proximity,
// ramparts) for every remote HTTP/SSE server, even with Docker running and
// deep-scan enabled — and the skip message's remedy ("set isolation.mode to
// \"docker\" for this server") was a dead end, because the same gate ignored
// that override too.
//
// Once this helper returns "docker", the engine contract takes over; that half
// is pinned by TestEngineResolveScannersDockerModeUnaffected and
// TestEngineResolveScannersSkipsDockerUnderSandbox in internal/security/scanner.
func TestScannerIsolationModeForRemoteServer(t *testing.T) {
	remote := func(iso *config.IsolationConfig) *config.ServerConfig {
		return &config.ServerConfig{
			Name:      "remote",
			URL:       "https://example.test/mcp",
			Protocol:  "http",
			Isolation: iso,
		}
	}

	tests := []struct {
		name   string
		cfg    *config.Config
		server string
		want   string
	}{
		{
			// RED before the fix: returned "none".
			name: "remote server inherits a global docker mode",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
				Servers:         []*config.ServerConfig{remote(nil)},
			},
			server: "remote",
			want:   "docker",
		},
		{
			// RED before the fix: returned "none" — the dead-end remedy.
			name: "remote server pinned to isolation.mode:docker while global is off",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Mode: config.IsolationModeNone},
				Servers: []*config.ServerConfig{remote(&config.IsolationConfig{
					Mode: func() *config.IsolationMode { m := config.IsolationModeDocker; return &m }(),
				})},
			},
			server: "remote",
			want:   "docker",
		},
		{
			// Security policy (1): an explicit opt-out still skips Docker scanners.
			name: "remote server pinned to isolation.mode:none",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
				Servers: []*config.ServerConfig{remote(&config.IsolationConfig{
					Mode: func() *config.IsolationMode { m := config.IsolationModeNone; return &m }(),
				})},
			},
			server: "remote",
			want:   "none",
		},
		{
			name: "remote server with isolation.enabled:false",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
				Servers:         []*config.ServerConfig{remote(&config.IsolationConfig{Enabled: config.BoolPtr(false)})},
			},
			server: "remote",
			want:   "none",
		},
		{
			// Security policy (2): a sandbox host runs no Docker for scanners.
			name: "remote server under a global sandbox mode",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Mode: config.IsolationModeSandbox},
				Servers:         []*config.ServerConfig{remote(nil)},
			},
			server: "remote",
			want:   "sandbox",
		},
		{
			name: "stdio server is unaffected",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
				Servers:         []*config.ServerConfig{{Name: "npx-srv", Command: "npx", Protocol: "stdio"}},
			},
			server: "npx-srv",
			want:   "docker",
		},
		{
			name: "unknown server falls back to the engine-wide default",
			cfg: &config.Config{
				DockerIsolation: &config.DockerIsolationConfig{Enabled: true},
				Servers:         []*config.ServerConfig{remote(nil)},
			},
			server: "absent",
			want:   "",
		},
		{
			name:   "nil config falls back to the engine-wide default",
			cfg:    nil,
			server: "remote",
			want:   "",
		},
		{
			name:   "config without a docker_isolation block falls back",
			cfg:    &config.Config{Servers: []*config.ServerConfig{remote(nil)}},
			server: "remote",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scannerIsolationModeFor(tt.cfg, tt.server); got != tt.want {
				t.Errorf("scannerIsolationModeFor(%q) = %q, want %q", tt.server, got, tt.want)
			}
		})
	}
}
