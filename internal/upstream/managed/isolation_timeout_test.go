package managed

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func isoModePtr(m config.IsolationMode) *config.IsolationMode { return &m }

// TestIsDockerIsolated_MatchesResolver is the "one algorithm" invariant for the
// connect-timeout selection: IsDockerIsolated picks the long Docker timeout
// (image pull + package install) instead of the short stdio one, so it must
// agree with the resolver the SPAWN path branches on — config.ResolveIsolation.
//
// The predicate predated isolation MODES and only read the two legacy booleans,
// so it disagreed with the spawn decision in both directions: a per-server
// `mode: "docker"` override is honoured at spawn even over a legacy
// `enabled: false`, but was given the SHORT timeout and could time out mid-pull;
// while a `mode: "sandbox"` server — and a server whose command already invokes
// docker, which is never double-wrapped — were given the long Docker timeout
// they have no use for, stretching every failed connect.
func TestIsDockerIsolated_MatchesResolver(t *testing.T) {
	optOut, optIn := false, true

	tests := []struct {
		name   string
		global *config.DockerIsolationConfig
		srv    *config.ServerConfig
		want   bool
	}{
		{
			name:   "per-server docker mode wins over a legacy opt-out and an off global",
			global: &config.DockerIsolationConfig{Enabled: false},
			srv: &config.ServerConfig{
				Name:      "moded",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Mode: isoModePtr(config.IsolationModeDocker), Enabled: &optOut},
			},
			want: true,
		},
		{
			name:   "global mode docker with the legacy bool left false",
			global: &config.DockerIsolationConfig{Mode: config.IsolationModeDocker},
			srv:    &config.ServerConfig{Name: "inheriting", Command: "npx"},
			want:   true,
		},
		{
			name:   "sandbox mode under global docker is not containerised",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv: &config.ServerConfig{
				Name:      "sandboxed",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Mode: isoModePtr(config.IsolationModeSandbox)},
			},
			want: false,
		},
		{
			name:   "a server that already runs docker is never double-wrapped",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv:    &config.ServerConfig{Name: "dockerised", Command: "docker"},
			want:   false,
		},
		{
			name:   "legacy opt-out still wins when there is no mode override",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv: &config.ServerConfig{
				Name:      "opted-out",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Enabled: &optOut},
			},
			want: false,
		},
		{
			name:   "legacy opt-in is ignored while the global mode is none",
			global: &config.DockerIsolationConfig{Enabled: false},
			srv: &config.ServerConfig{
				Name:      "opted-in",
				Command:   "npx",
				Isolation: &config.IsolationConfig{Enabled: &optIn},
			},
			want: false,
		},
		{
			name:   "inheriting stdio server under global docker",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv:    &config.ServerConfig{Name: "plain", Command: "uvx"},
			want:   true,
		},
		{
			name:   "http server has no child process to isolate",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv:    &config.ServerConfig{Name: "remote", URL: "https://example.test/mcp"},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolved := config.ResolveIsolation(tc.global, tc.srv)
			wantDocker := resolved.Mode == config.IsolationModeDocker
			if wantDocker != tc.want {
				t.Fatalf("test case expectation disagrees with the resolver: resolver says docker=%v (mode=%q source=%q), case wants %v",
					wantDocker, resolved.Mode, resolved.Source, tc.want)
			}

			mc := &Client{logger: zap.NewNop()}
			mc.SetConfig(tc.srv)
			mc.globalConfig.Store(&config.Config{DockerIsolation: tc.global})

			if got := mc.IsDockerIsolated(); got != tc.want {
				t.Errorf("IsDockerIsolated() = %v, want %v (resolver mode=%q source=%q)",
					got, tc.want, resolved.Mode, resolved.Source)
			}
		})
	}
}

// TestIsDockerIsolated_NilGlobalConfig keeps the hand-constructed-client path
// (no global config stored at all) answering false rather than panicking.
func TestIsDockerIsolated_NilGlobalConfig(t *testing.T) {
	mc := &Client{logger: zap.NewNop()}
	mc.SetConfig(&config.ServerConfig{Name: "no-global", Command: "npx"})
	if mc.IsDockerIsolated() {
		t.Error("IsDockerIsolated() = true with no global config stored, want false")
	}
}
