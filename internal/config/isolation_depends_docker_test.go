package config

import "testing"

// TestServerDependsOnDocker separates the two questions that used to share one
// predicate (GH #1142): "which wrapper does the spawn path apply" (Mode) versus
// "does starting this server need a working Docker daemon" (this function).
// They agree everywhere except on a server that already invokes docker itself,
// which we must never double-wrap yet which still pays image-pull latency.
func TestServerDependsOnDocker(t *testing.T) {
	dockerMode, sandboxMode := IsolationModeDocker, IsolationModeSandbox
	optOut := false

	tests := []struct {
		name     string
		global   *DockerIsolationConfig
		srv      *ServerConfig
		wantMode IsolationMode
		want     bool
	}{
		{
			name:     "we containerise it",
			global:   &DockerIsolationConfig{Enabled: true},
			srv:      &ServerConfig{Name: "plain", Command: "npx"},
			wantMode: IsolationModeDocker,
			want:     true,
		},
		{
			name:     "it containerises itself: mode is none, the dependency is real",
			global:   &DockerIsolationConfig{Enabled: true},
			srv:      &ServerConfig{Name: "dockerised", Command: "docker", Args: []string{"run", "-i", "mcp/foo"}},
			wantMode: IsolationModeNone,
			want:     true,
		},
		{
			name:     "it containerises itself even with isolation globally off",
			global:   &DockerIsolationConfig{Enabled: false},
			srv:      &ServerConfig{Name: "dockerised", Command: "docker"},
			wantMode: IsolationModeNone,
			want:     true,
		},
		{
			name:     "sandbox mode needs no daemon",
			global:   &DockerIsolationConfig{Enabled: true},
			srv:      &ServerConfig{Name: "sandboxed", Command: "npx", Isolation: &IsolationConfig{Mode: &sandboxMode}},
			wantMode: IsolationModeSandbox,
			want:     false,
		},
		{
			name:     "per-server docker mode over an off global",
			global:   &DockerIsolationConfig{Enabled: false},
			srv:      &ServerConfig{Name: "moded", Command: "npx", Isolation: &IsolationConfig{Mode: &dockerMode, Enabled: &optOut}},
			wantMode: IsolationModeDocker,
			want:     true,
		},
		{
			name:     "http server has no child process at all",
			global:   &DockerIsolationConfig{Enabled: true},
			srv:      &ServerConfig{Name: "remote", URL: "https://example.test/mcp"},
			wantMode: IsolationModeNone,
			want:     false,
		},
		{
			name: "nil server",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.srv != nil {
				if got := ResolveIsolation(tc.global, tc.srv).Mode; got != tc.wantMode {
					t.Fatalf("precondition: ResolveIsolation().Mode = %q, want %q", got, tc.wantMode)
				}
			}
			if got := ServerDependsOnDocker(tc.global, tc.srv); got != tc.want {
				t.Errorf("ServerDependsOnDocker() = %v, want %v", got, tc.want)
			}
		})
	}
}
