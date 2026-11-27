package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
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
		// serverEnv: env is the server's own, which our docker commands get too.
		serverEnv bool
		workDir   string
		other     bool
	}{
		{name: "nothing set", login: map[string]string{}},
		{name: "set for mcpproxy itself", own: "tcp://a", env: []string{host + "=tcp://a"}, login: map[string]string{}},
		{name: "only in docker run's env", env: []string{host + "=tcp://a"}, login: map[string]string{}, other: true},
		// With DOCKER_HOST in mcpproxy's own env, docker runs directly, without the login profile.
		{name: "in the server's env, docker run directly", own: "tcp://a", env: []string{host + "=tcp://b"}, serverEnv: true, login: map[string]string{}},
		// The profile may change or unset it (`unset DOCKER_HOST`), and login cannot show that.
		{name: "in the server's env, docker run through the login shell", env: []string{host + "=tcp://a"}, serverEnv: true, login: map[string]string{}, other: unix},
		{name: "relative DOCKER_CONFIG without working_dir", own: "tcp://a", env: []string{"DOCKER_CONFIG=cfg"}, serverEnv: true, login: map[string]string{}},
		{name: "relative DOCKER_CONFIG with working_dir", own: "tcp://a", env: []string{"DOCKER_CONFIG=cfg"}, serverEnv: true, workDir: "/srv", login: map[string]string{}, other: true},
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
			c.config.WorkingDir = tt.workDir
			if tt.serverEnv {
				vars := map[string]string{}
				for _, kv := range tt.env {
					k, v, _ := strings.Cut(kv, "=")
					vars[k] = v
				}
				c.envManager = secureenv.NewManager(&secureenv.EnvConfig{CustomVars: vars})
			}
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

// A probe that answers after the answers were forgotten must not put its answer back.
func TestDockerIsolation_ProbeRunningWhileForgottenIsNotRemembered(t *testing.T) {
	dir := t.TempDir()
	log, gate := filepath.Join(dir, "calls.log"), filepath.Join(dir, "gate")
	// The shim answers only once the gate exists, so the test decides when the probe ends.
	name, script := "docker", "#!/bin/sh\necho \"$1\" >> '"+log+"'\nwhile [ ! -e '"+gate+"' ]; do /bin/sleep 0.1; done\necho docker-init\n"
	if runtime.GOOS == osWindows {
		ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "ping.exe")
		name, script = "docker.cmd", "@echo off\r\necho %1>>\""+log+"\"\r\n:wait\r\nif exist \""+gate+"\" goto done\r\n\""+ping+"\" -n 2 127.0.0.1 >nul\r\ngoto wait\r\n:done\r\necho docker-init\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	t.Setenv("DOCKER_HOST", "")
	forceDockerDaemonEnvGOOS(t, osDarwin)
	useRealDockerResolver(t)
	clearDockerInitKnown()
	t.Cleanup(clearDockerInitKnown)

	probes := func() int {
		raw, _ := os.ReadFile(log)
		return strings.Count(string(raw), "version")
	}
	c := newIsolatedTestClient()
	first := make(chan bool)
	go func() { first <- c.dockerHasInit(context.Background(), os.Environ()) }()
	require.Eventually(t, func() bool { return probes() == 1 }, 5*time.Second, 10*time.Millisecond)

	clearDockerInitKnown() // a failed spawn forgets while the probe still runs
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.True(t, <-first)

	require.True(t, c.dockerHasInit(context.Background(), os.Environ()))
	require.Equal(t, 2, probes(), "the daemon is asked again")
}
