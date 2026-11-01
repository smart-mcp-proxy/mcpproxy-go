package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
)

// Issue #1809: a server's `env` values (including resolved ${keyring:…},
// ${env:…} and ${file:…} secrets) must never appear in the docker process's
// argv, where `ps aux` / `/proc/<pid>/cmdline` expose them to every local
// user. Docker's `-e KEY` (no `=`) form takes the value from the docker CLI's
// own environment, which the spawn paths already populate from config.Env.

const argvSecret = "xoxb-secret"

func assertNoSecretInArgv(t *testing.T, args []string) {
	t.Helper()
	for _, a := range args {
		assert.NotContains(t, a, argvSecret,
			"env value must not appear in docker argv, got: %v", args)
	}
}

func TestInjectEnvVarsIntoDockerArgs_KeyOnlyNoValueInArgv(t *testing.T) {
	c := newUserSuppliedDockerTestClient()
	c.config.Env = map[string]string{"SLACK_TOKEN": argvSecret, "A_FIRST": "v1"}

	args := c.injectEnvVarsIntoDockerArgs(c.config.Args, c.config.Env)

	assertNoSecretInArgv(t, args)
	assert.Equal(t, cmdRun, args[0])
	// Keys are emitted sorted so argv (and the log line) is deterministic.
	assert.Equal(t, []string{cmdRun, "-e", "A_FIRST", "-e", "SLACK_TOKEN", "-i", "--rm", "mcp/example"}, args)
}

func TestBuildDockerArgs_IsolationKeyOnlyNoValueInArgv(t *testing.T) {
	im := NewIsolationManager(config.DefaultDockerIsolationConfig())
	serverConfig := &config.ServerConfig{
		Name:    "iso-server",
		Command: "python",
		Args:    []string{"-m", "mcp_server"},
		Env:     map[string]string{"SLACK_TOKEN": argvSecret},
	}

	args, err := im.BuildDockerArgs(serverConfig, "python")
	require.NoError(t, err)

	assertNoSecretInArgv(t, args)
	assert.Subset(t, args, []string{"-e", "SLACK_TOKEN"},
		"isolation argv must carry the key-only -e flag, got: %v", args)
}

func TestBuildLauncherCmd_UserDockerRun_SecretInEnvNotArgv(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newUserSuppliedDockerTestClient()
	c.envManager = secureenv.NewManager(nil)

	cmd, _, _, err := c.buildLauncherCmd(context.Background(), true)
	require.NoError(t, err)

	assertNoSecretInArgv(t, cmd.Args)
	assert.Subset(t, cmd.Args, []string{"-e", "SLACK_TOKEN"})
	assert.Contains(t, cmd.Env, "SLACK_TOKEN="+argvSecret,
		"the value must reach docker through its environment, got env: %v", cmd.Env)
}

func TestBuildLauncherCmd_UserDockerRun_ShellWrapSecretInEnvNotArgv(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return "", fmt.Errorf("docker not found") }

	c := newUserSuppliedDockerTestClient()
	c.envManager = secureenv.NewManager(nil)

	cmd, _, _, err := c.buildLauncherCmd(context.Background(), true)
	require.NoError(t, err)

	assertNoSecretInArgv(t, cmd.Args)
	last := cmd.Args[len(cmd.Args)-1]
	assert.True(t, strings.Contains(last, "-e SLACK_TOKEN "),
		"shell command string must carry the key-only -e flag, got: %s", last)
	assert.Contains(t, cmd.Env, "SLACK_TOKEN="+argvSecret)
}
