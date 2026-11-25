package core

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// A container our own docker commands may not find must still get the docker client's SIGTERM.
func TestDockerIsolation_SigProxyOffOnlyOnOurOwnDaemon(t *testing.T) {
	const host = "DOCKER_HOST"
	// Windows has no login shell, and macOS runs docker directly (dockerDaemonEnvGuaranteed).
	unix := runtime.GOOS != osWindows && runtime.GOOS != osDarwin
	tests := []struct {
		name  string
		own   string
		env   []string
		login map[string]string
		other bool
	}{
		{name: "nothing set", login: map[string]string{}},
		{name: "set for mcpproxy itself", own: "tcp://a", env: []string{host + "=tcp://a"}, login: map[string]string{}},
		{name: "only in the server's env", env: []string{host + "=tcp://a"}, login: map[string]string{}, other: true},
		{name: "only in the login profile", login: map[string]string{host: "tcp://a"}, other: unix},
		{name: "context in the login profile", login: map[string]string{"DOCKER_CONTEXT": "podman"}, other: unix},
		{name: "login profile unknown", login: nil, other: unix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{host, "DOCKER_CONTEXT", "DOCKER_CONFIG"} {
				t.Setenv(key, "")
			}
			t.Setenv(host, tt.own)
			forceDockerDaemonEnvGOOS(t, runtime.GOOS)
			orig := loginShellEnv
			t.Cleanup(func() { loginShellEnv = orig })
			loginShellEnv = func(*zap.Logger) map[string]string { return tt.login }

			c := newIsolatedTestClient()
			c.dockerOtherDaemon = c.dockerRunMayUseOtherDaemon(tt.env)
			require.Equal(t, tt.other, c.dockerOtherDaemon)

			_, args, _, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)
			require.Equal(t, !tt.other, strings.Contains(strings.Join(args, " "), "--sig-proxy=false"))
		})
	}
}

// A remembered answer can be about another daemon, so a failed spawn must ask again.
func TestDockerIsolation_FailedSpawnForgetsInit(t *testing.T) {
	log := installDockerReporting(t, "Engine", "docker-init")
	for _, key := range []string{"DOCKER_CONTEXT", "DOCKER_CONFIG"} {
		t.Setenv(key, "")
	}
	global := config.DefaultConfig()
	global.DockerIsolation = config.DefaultDockerIsolationConfig()
	global.DockerIsolation.Enabled = true
	srv := &config.ServerConfig{Name: "smoke", Command: "uvx", Args: []string{"some-server"}, Enabled: true}
	c, err := NewClient("smoke", srv, zap.NewNop(), nil, global, nil, secret.NewResolver())
	require.NoError(t, err)
	for range 2 { // the shim's "container" is no MCP server
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		require.Error(t, c.Connect(ctx))
		cancel()
	}
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	probes := slices.DeleteFunc(strings.Fields(string(raw)), func(s string) bool { return s != "version" })
	require.Len(t, probes, 2, "one probe per failed spawn")
}
