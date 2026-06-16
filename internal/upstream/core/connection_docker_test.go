package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newIsolatedTestClient() *Client {
	return &Client{
		config: &config.ServerConfig{
			Name:    "iso-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
		},
		logger:           zap.NewNop(),
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
	}
}

// TestSetupDockerIsolationUsesResolvedAbsolutePath verifies the root-cause fix
// for #696: an isolated server is spawned with docker invoked by its resolved
// ABSOLUTE path (bypassing PATH), not the bare "docker" command that the
// login-shell PATH may be unable to resolve.
func TestSetupDockerIsolationUsesResolvedAbsolutePath(t *testing.T) {
	const fakeDocker = "/opt/fake/Docker.app/Contents/Resources/bin/docker"

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	shellCmd, shellArgs := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.NotEmpty(t, shellCmd, "shell command must not be empty")
	require.NotEmpty(t, shellArgs, "shell args must not be empty")

	// The shell-wrapped command string is the last shell arg.
	cmdStr := shellArgs[len(shellArgs)-1]
	assert.Contains(t, cmdStr, fakeDocker,
		"docker must be invoked by its resolved absolute path, got: %s", cmdStr)
	assert.False(t, strings.HasPrefix(cmdStr, "docker run"),
		"docker must not be invoked as the bare 'docker' command, got: %s", cmdStr)
}

// TestSetupDockerIsolationCidfileInsertionWithAbsolutePath verifies the
// existing cidfile-insertion logic still matches when docker is referenced by
// its absolute path (the trailing "docker run" substring is preserved).
func TestSetupDockerIsolationCidfileInsertionWithAbsolutePath(t *testing.T) {
	const fakeDocker = "/opt/fake/Docker.app/Contents/Resources/bin/docker"

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	_, shellArgs := c.setupDockerIsolation(c.config.Command, c.config.Args)
	withCid := c.insertCidfileIntoShellDockerCommand(shellArgs, "/tmp/cid.txt")

	cmdStr := withCid[len(withCid)-1]
	assert.Contains(t, cmdStr, fakeDocker+" run --cidfile /tmp/cid.txt",
		"cidfile must be inserted right after the absolute-path docker run, got: %s", cmdStr)
}

// TestInsertCidfileWindowsCmdFormat verifies cidfile insertion works with the
// Windows cmd.exe shell-wrap format: ["/c", "docker run …"] (second-to-last
// arg is "/c", not "-c"). This is a cross-platform unit test for the guard
// that previously rejected the /c flag as an unrecognised format.
func TestInsertCidfileWindowsCmdFormat(t *testing.T) {
	const fakeDocker = "/opt/fake/bin/docker"
	c := newIsolatedTestClient()
	// Simulate Windows cmd.exe output: shell returns ["/c", cmdStr]
	windowsShellArgs := []string{"/c", fakeDocker + " run --rm -i ghcr.io/some/image python -m srv"}
	withCid := c.insertCidfileIntoShellDockerCommand(windowsShellArgs, "/tmp/cid.txt")
	cmdStr := withCid[len(withCid)-1]
	assert.Contains(t, cmdStr, fakeDocker+" run --cidfile /tmp/cid.txt",
		"cidfile must be inserted in Windows cmd /c format too, got: %s", cmdStr)
}

// TestSetupDockerIsolationFallsBackToBareDocker verifies that when docker
// cannot be resolved to an absolute path, the spawn falls back to the bare
// "docker" command (preserving prior behavior / login-shell resolution).
func TestSetupDockerIsolationFallsBackToBareDocker(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) {
		return "", fmt.Errorf("docker not found")
	}

	c := newIsolatedTestClient()
	_, shellArgs := c.setupDockerIsolation(c.config.Command, c.config.Args)

	cmdStr := shellArgs[len(shellArgs)-1]
	assert.True(t, strings.HasPrefix(cmdStr, "docker run"),
		"on resolution failure the spawn must fall back to bare 'docker', got: %s", cmdStr)
}
