package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestResolveScannerModeDelegatesToConfig pins the manager wrapper to the
// config-level algorithm and, on the same inputs, re-asserts that the
// process-spawn answers are untouched (GH #1303).
//
// The two questions are deliberately different: ResolveMode/ShouldIsolate say
// how this server's own child process is launched; ResolveScannerMode says
// whether the security scanner may run its own short-lived Docker containers
// against tool definitions that were already captured over MCP.
func TestResolveScannerModeDelegatesToConfig(t *testing.T) {
	remote := &config.ServerConfig{Name: "remote", URL: "https://example.test/mcp", Protocol: "http"}
	stdio := &config.ServerConfig{Name: "npx-srv", Command: "npx", Protocol: "stdio"}
	optOut := &config.ServerConfig{
		Name: "remote-optout", URL: "https://example.test/mcp", Protocol: "http",
		Isolation: &config.IsolationConfig{Enabled: config.BoolPtr(false)},
	}

	tests := []struct {
		name   string
		global *config.DockerIsolationConfig
		server *config.ServerConfig

		wantScannerMode config.IsolationMode
		wantSpawnMode   config.IsolationMode
		wantShould      bool
	}{
		{
			name:            "remote server inherits the global docker mode for scanners only",
			global:          &config.DockerIsolationConfig{Enabled: true},
			server:          remote,
			wantScannerMode: config.IsolationModeDocker,
			wantSpawnMode:   config.IsolationModeNone,
			wantShould:      false,
		},
		{
			name:            "remote server opt-out still blocks docker scanners",
			global:          &config.DockerIsolationConfig{Enabled: true},
			server:          optOut,
			wantScannerMode: config.IsolationModeNone,
			wantSpawnMode:   config.IsolationModeNone,
			wantShould:      false,
		},
		{
			name:            "stdio server answers both questions the same way",
			global:          &config.DockerIsolationConfig{Enabled: true},
			server:          stdio,
			wantScannerMode: config.IsolationModeDocker,
			wantSpawnMode:   config.IsolationModeDocker,
			wantShould:      true,
		},
		{
			name:            "global off blocks both",
			global:          &config.DockerIsolationConfig{Mode: config.IsolationModeNone},
			server:          stdio,
			wantScannerMode: config.IsolationModeNone,
			wantSpawnMode:   config.IsolationModeNone,
			wantShould:      false,
		},
		{
			name:            "nil server inherits the global mode without panicking",
			global:          &config.DockerIsolationConfig{Enabled: true},
			server:          nil,
			wantScannerMode: config.IsolationModeDocker,
			wantSpawnMode:   config.IsolationModeNone,
			wantShould:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			im := NewIsolationManager(tt.global)

			wantMode, wantSource := config.ResolveScannerIsolationMode(tt.global, tt.server)
			gotMode, gotSource := im.ResolveScannerMode(tt.server)
			if gotMode != wantMode || gotSource != wantSource {
				t.Errorf("ResolveScannerMode() = (%q, %q), config.ResolveScannerIsolationMode() = (%q, %q)",
					gotMode, gotSource, wantMode, wantSource)
			}
			if gotMode != tt.wantScannerMode {
				t.Errorf("scanner mode = %q, want %q", gotMode, tt.wantScannerMode)
			}

			// Regression fence: the process-spawn path is unchanged.
			if got := im.ResolveMode(tt.server); got != tt.wantSpawnMode {
				t.Errorf("ResolveMode() = %q, want %q (process isolation must not change)", got, tt.wantSpawnMode)
			}
			if got := im.ShouldIsolate(tt.server); got != tt.wantShould {
				t.Errorf("ShouldIsolate() = %v, want %v", got, tt.wantShould)
			}
		})
	}
}
