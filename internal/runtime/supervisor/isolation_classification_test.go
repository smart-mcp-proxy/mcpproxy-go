package supervisor

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

func modePtr(m config.IsolationMode) *config.IsolationMode { return &m }

// TestUsesDockerIsolation_MatchesResolver is the "one algorithm" invariant for
// the diagnostics classifier: whether the supervisor attributes a spawn failure
// to a DOCKER remediation code must agree with the resolver the SPAWN path uses
// (config.ResolveIsolation), not with a hand-rolled mirror of it.
//
// The mirror predated isolation MODES and only ever looked at the two legacy
// booleans, so it disagreed with the spawn decision in both directions:
//   - a per-server `mode: "docker"` override wins outright in the resolver
//     (even over a legacy `enabled: false` and a global that is off), so such a
//     server IS Docker-spawned but was classified non-Docker — its docker exec
//     failures got a generic stdio ENOENT remediation instead of the #696
//     Docker guidance, and the image/default-image enrichment was dropped;
//   - a `mode: "sandbox"` server under global Docker isolation is NOT
//     containerised, but was classified Docker-isolated and offered Docker
//     remediation for a Landlock failure.
func TestUsesDockerIsolation_MatchesResolver(t *testing.T) {
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
				Isolation: &config.IsolationConfig{Mode: modePtr(config.IsolationModeDocker), Enabled: &optOut},
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
				Isolation: &config.IsolationConfig{Mode: modePtr(config.IsolationModeSandbox)},
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
			name:   "a server that already runs docker is never double-wrapped",
			global: &config.DockerIsolationConfig{Enabled: true},
			srv:    &config.ServerConfig{Name: "dockerised", Command: "docker"},
			want:   false,
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
			cfg := &config.Config{DockerIsolation: tc.global, Servers: []*config.ServerConfig{tc.srv}}
			configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
			defer configSvc.Close()

			mockUpstream := NewMockUpstreamAdapter()
			defer mockUpstream.Close()

			s := New(configSvc, mockUpstream, zap.NewNop())

			// The resolver is the reference answer: it is what the spawn path
			// branches on.
			resolved := config.ResolveIsolation(tc.global, tc.srv)
			wantDocker := resolved.Mode == config.IsolationModeDocker
			if wantDocker != tc.want {
				t.Fatalf("test case expectation disagrees with the resolver: resolver says docker=%v (mode=%q source=%q), case wants %v",
					wantDocker, resolved.Mode, resolved.Source, tc.want)
			}

			if got := s.usesDockerIsolation(tc.srv); got != tc.want {
				t.Errorf("usesDockerIsolation() = %v, want %v (resolver mode=%q source=%q)",
					got, tc.want, resolved.Mode, resolved.Source)
			}

			hints := s.classifierHints(tc.srv, "stdio")
			if hints.DockerIsolated != tc.want {
				t.Errorf("classifierHints().DockerIsolated = %v, want %v", hints.DockerIsolated, tc.want)
			}
		})
	}
}

// The DockerMissingToolchain remediation can only say whether mcpproxy's
// automatic git-capable image selection covers the failure if the server's args
// reach the classifier (#1143/#1144).
func TestClassifierHints_CarryArgsForGitDependencies(t *testing.T) {
	srv := &config.ServerConfig{
		Name:    "git-server",
		Command: "uvx",
		Args:    []string{"--from", "srv@git+https://github.com/o/r", "srv"},
	}
	cfg := &config.Config{
		DockerIsolation: &config.DockerIsolationConfig{Enabled: true, DefaultImages: config.DefaultDockerIsolationConfig().DefaultImages},
		Servers:         []*config.ServerConfig{srv},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	mockUpstream := NewMockUpstreamAdapter()
	defer mockUpstream.Close()

	s := New(configSvc, mockUpstream, zap.NewNop())

	hints := s.classifierHints(srv, "stdio")
	if !hints.DockerIsolated {
		t.Fatal("expected the server to be classified Docker-isolated")
	}
	if len(hints.DockerArgs) != len(srv.Args) {
		t.Fatalf("classifierHints().DockerArgs = %v, want %v", hints.DockerArgs, srv.Args)
	}
	if got := hints.DockerDefaultImages[config.GitCapableImageKey]; got != config.DefaultGitCapableImage {
		t.Errorf("hints do not carry the git-capable default image: %q", got)
	}
}
