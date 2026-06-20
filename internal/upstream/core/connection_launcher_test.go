package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestBuildLauncherCmdUserSuppliedDockerRunDirectExec is the MCP-2881 root-cause
// assertion. A user-supplied `docker run` upstream launched via the LAUNCHER
// path (HTTP/SSE transport that also sets Command, so mcpproxy owns the process
// lifecycle) must reuse the SAME resolve→direct-exec + bundle-dir PATH wiring as
// the stdio path. Before this fix the launcher's else branch always shell-wrapped
// bare `docker` and never prepended the docker bundle dir to the child PATH, so
// the #715 credential-helper fix never reached this entrypoint: an uncached image
// pull could still fail with `docker-credential-desktop ... not found in $PATH`.
func TestBuildLauncherCmdUserSuppliedDockerRunDirectExec(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)
	forceDockerDaemonEnvGOOS(t, osDarwin) // daemon env guaranteed via startup hydration

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newUserSuppliedDockerTestClient()
	c.envManager = secureenv.NewManager(nil)

	cmd, isDocker, cidFile, err := c.buildLauncherCmd(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, isDocker)

	// Direct-exec of the resolved ABSOLUTE docker binary — NOT a `$SHELL -l -c` wrap.
	assert.Equal(t, fakeDocker, cmd.Path,
		"launcher user-supplied docker run must direct-exec the resolved absolute docker, got: %s", cmd.Path)
	require.GreaterOrEqual(t, len(cmd.Args), 2)
	assert.Equal(t, fakeDocker, cmd.Args[0])
	assert.Equal(t, cmdRun, cmd.Args[1],
		"second argv token must be the raw 'run' token, not a shell -c string, got: %v", cmd.Args)

	// Injected -e env flag must survive into the docker run argv.
	assert.Subset(t, cmd.Args, []string{"-e", "SLACK_TOKEN=xoxb-secret"},
		"injected -e env flags must be present in the direct-exec argv, got: %v", cmd.Args)

	// cidfile inserted right after 'run' via the args-based helper.
	require.NotEmpty(t, cidFile)
	assert.Subset(t, cmd.Args, []string{"--cidfile", cidFile},
		"cidfile must be inserted via the args-based helper on the direct-exec path, got: %v", cmd.Args)

	// The docker bundle dir must be prepended to the spawn env PATH (#715).
	wantDir := filepath.Dir(fakeDocker)
	var path string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	parts := filepath.SplitList(path)
	require.NotEmpty(t, parts, "spawn env must carry a PATH entry")
	assert.Equal(t, wantDir, parts[0],
		"docker bundle dir must be prepended to the launcher spawn env PATH, got: %s", path)
}

// TestBuildLauncherCmdUserSuppliedDockerRunShellWrapFallback verifies that when
// docker cannot be resolved to a verified absolute executable, the launcher path
// falls back to a login-shell wrap (preserving #696's last-resort PATH lookup),
// mirroring the stdio path — rather than producing a broken direct-exec.
func TestBuildLauncherCmdUserSuppliedDockerRunShellWrapFallback(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) {
		return "", fmt.Errorf("docker not found")
	}

	c := newUserSuppliedDockerTestClient()
	c.envManager = secureenv.NewManager(nil)

	cmd, isDocker, _, err := c.buildLauncherCmd(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, isDocker)
	assert.NotEqual(t, cmdDocker, cmd.Path,
		"shell-wrap fallback exec's the login shell, not bare 'docker', got: %s", cmd.Path)

	last := cmd.Args[len(cmd.Args)-1]
	assert.Contains(t, last, "docker run",
		"fallback must shell-wrap a 'docker run' command string, got: %s", last)
	assert.Contains(t, last, "-e SLACK_TOKEN=xoxb-secret",
		"injected -e env flag must survive into the shell command string, got: %s", last)
}

// TestBuildLauncherCmdPlainCommandStillShellWraps is the no-regression guard: a
// plain (non-docker) launcher command must still be shell-wrapped for login-env
// inheritance and must NOT receive docker PATH augmentation or a cidfile.
func TestBuildLauncherCmdPlainCommandStillShellWraps(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{
			Name:    "plain-launcher",
			Command: "my-server",
			Args:    []string{"--port", "9000"},
			URL:     "http://127.0.0.1:9000",
		},
		logger:           zap.NewNop(),
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
		envManager:       secureenv.NewManager(nil),
	}

	cmd, isDocker, cidFile, err := c.buildLauncherCmd(context.Background(), false)
	require.NoError(t, err)
	assert.False(t, isDocker)
	assert.Empty(t, cidFile, "non-docker launcher command must not create a cidfile")

	last := cmd.Args[len(cmd.Args)-1]
	assert.Contains(t, last, "my-server --port 9000",
		"plain command must be shell-wrapped, got: %v", cmd.Args)
}
