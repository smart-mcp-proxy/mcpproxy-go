package managed

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func isoModePtr(m config.IsolationMode) *config.IsolationMode { return &m }

// TestDependsOnDocker_MatchesResolver is the "one algorithm" invariant for the
// connect-timeout selection: DependsOnDocker picks the long Docker budget
// (image pull + package install) instead of the short stdio one, so for every
// server we CONTAINERISE it must agree with the resolver the SPAWN path
// branches on — config.ResolveIsolation.
//
// The predicate predated isolation MODES and only read the two legacy booleans,
// so it disagreed with the spawn decision in both directions: a per-server
// `mode: "docker"` override is honoured at spawn even over a legacy
// `enabled: false`, but was given the SHORT timeout and could time out mid-pull;
// while a `mode: "sandbox"` server was given the long Docker timeout it has no
// use for, stretching every failed connect.
//
// The one deliberate DIVERGENCE from the resolver is a server whose own command
// is `docker`: the resolver reports mode=none there because we must never
// double-wrap it, but it still pays image-pull latency, so it still needs the
// long budget. That case is asserted separately in
// TestDependsOnDocker_ServerRunningDockerItself.
func TestDependsOnDocker_MatchesResolver(t *testing.T) {
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

			if got := mc.DependsOnDocker(); got != tc.want {
				t.Errorf("DependsOnDocker() = %v, want %v (resolver mode=%q source=%q)",
					got, tc.want, resolved.Mode, resolved.Source)
			}
		})
	}
}

// TestDependsOnDocker_ServerRunningDockerItself pins the deliberate divergence
// from config.ResolveIsolation described above. The resolver's already-docker
// structural gate answers the SPAWN question ("do we wrap this?" — no, never
// double-wrap), but the connect budget answers a different one ("will this
// server sit in an image pull before it answers initialize?" — yes). Collapsing
// the two dropped such a server from the 3-minute floor to the resolved
// init_timeout (30s by default), where it can be killed mid-pull (GH #1142).
func TestDependsOnDocker_ServerRunningDockerItself(t *testing.T) {
	srv := &config.ServerConfig{
		Name:    "dockerised",
		Command: "docker",
		Args:    []string{"run", "-i", "--rm", "mcp/foo"},
	}

	for _, global := range []*config.DockerIsolationConfig{
		{Enabled: true},
		{Enabled: false},
		nil,
	} {
		resolved := config.ResolveIsolation(global, srv)
		if resolved.Mode == config.IsolationModeDocker {
			t.Fatalf("precondition: the resolver must NOT report mode=docker for a docker command, got %q (source %q)",
				resolved.Mode, resolved.Source)
		}

		mc := &Client{logger: zap.NewNop()}
		mc.SetConfig(srv)
		mc.globalConfig.Store(&config.Config{DockerIsolation: global})

		if !mc.DependsOnDocker() {
			t.Errorf("DependsOnDocker() = false for a command:\"docker\" server (global isolation %+v); it still pays image-pull latency", global)
		}
	}
}

// TestDependsOnDocker_NilGlobalConfig keeps the hand-constructed-client path
// (no global config stored at all) answering false rather than panicking.
func TestDependsOnDocker_NilGlobalConfig(t *testing.T) {
	mc := &Client{logger: zap.NewNop()}
	mc.SetConfig(&config.ServerConfig{Name: "no-global", Command: "npx"})
	if mc.DependsOnDocker() {
		t.Error("DependsOnDocker() = true with no global config stored, want false")
	}
}
