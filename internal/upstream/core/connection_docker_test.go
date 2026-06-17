package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// writeFakeDockerExecutable writes a real, executable file under a fresh temp
// dir and returns its absolute path. The direct-exec guard (MCP-2753) only
// trusts a resolved value that is an ABSOLUTE path to an actual executable, so
// tests that exercise the direct-exec branch must point at a binary that
// genuinely exists with the exec bit set — a string that merely *looks* like a
// path is (correctly) rejected and shell-wrapped instead.
func writeFakeDockerExecutable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name = "docker.exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	return p
}

// TestSetupDockerIsolationExecsResolvedBinaryDirectly is the root-cause
// assertion for MCP-2753: on successful resolution to a verified absolute
// executable, the isolated server must be spawned by exec'ing the resolved
// docker binary DIRECTLY — no `$SHELL -l -c "<docker> run …"` indirection. A
// login shell re-derives PATH from rc files and can drop the Docker Desktop
// bundle dir, so a shell-wrapped absolute path is only a token whose lookup the
// shell can still override. Exec'ing the absolute path bypasses PATH entirely.
func TestSetupDockerIsolationExecsResolvedBinaryDirectly(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	cmd, args, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)

	assert.False(t, shellWrapped, "verified absolute executable must NOT shell-wrap the spawn")
	assert.Equal(t, fakeDocker, cmd,
		"docker must be exec'd by its resolved absolute path directly, got command: %s", cmd)
	require.NotEmpty(t, args, "docker args must not be empty")
	assert.Equal(t, cmdRun, args[0],
		"first docker arg must be 'run' (raw argv, not a shell -c string), got: %v", args)
	// No element may be a single shell command string wrapping the whole command.
	for _, a := range args {
		assert.NotContains(t, a, " run ",
			"args must be raw argv tokens, not a single shell command string, got: %v", args)
	}
}

// TestSetupDockerIsolationUsesResolvedAbsolutePath verifies the resolved
// ABSOLUTE path is used as the exec command (bypassing PATH), not the bare
// "docker" command that the login-shell PATH may be unable to resolve.
func TestSetupDockerIsolationUsesResolvedAbsolutePath(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	cmd, _, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)

	assert.False(t, shellWrapped)
	assert.Equal(t, fakeDocker, cmd,
		"docker must be invoked by its resolved absolute path, got: %s", cmd)
	assert.NotEqual(t, cmdDocker, cmd,
		"docker must not be invoked as the bare 'docker' command, got: %s", cmd)
}

// TestSetupDockerIsolationShellWrapsNonAbsoluteResolved is the Codex round-3
// P2 #1 guard: ResolveDockerPath's last resort runs `command -v docker` in the
// login shell, which can emit a shell function name, an alias, or a bare
// command name rather than an absolute path. Direct-exec'ing such a value would
// fail with "no such file or directory", so a non-absolute resolved value MUST
// fall back to the login-shell wrap (which can still resolve it interactively).
func TestSetupDockerIsolationShellWrapsNonAbsoluteResolved(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	// e.g. a shell builtin/alias/function name, or a bare command name.
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return "docker", nil }

	c := newIsolatedTestClient()
	cmd, shellArgs, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped, "a non-absolute resolved value must be shell-wrapped, not direct-exec'd")
	assert.NotEqual(t, "docker", cmd, "must not direct-exec a bare command name")
	require.NotEmpty(t, shellArgs)
	cmdStr := shellArgs[len(shellArgs)-1]
	assert.True(t, strings.HasPrefix(cmdStr, "docker run"),
		"shell fallback must wrap the bare 'docker' command, got: %s", cmdStr)
}

// TestSetupDockerIsolationShellWrapsNonExecutableResolved is the other half of
// the P2 #1 guard: an absolute path that does not exist (or is not executable)
// must not be direct-exec'd — it falls back to the login-shell wrap.
func TestSetupDockerIsolationShellWrapsNonExecutableResolved(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) {
		return "/no/such/path/docker", nil
	}

	c := newIsolatedTestClient()
	_, shellArgs, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped, "a non-existent absolute path must be shell-wrapped, not direct-exec'd")
	require.NotEmpty(t, shellArgs)
	cmdStr := shellArgs[len(shellArgs)-1]
	assert.True(t, strings.HasPrefix(cmdStr, "docker run"),
		"shell fallback must wrap the bare 'docker' command, got: %s", cmdStr)
}

// TestSetupDockerIsolationCidfileInsertionWithAbsolutePath verifies the
// direct-exec cidfile-insertion logic inserts --cidfile right after the "run"
// token in the raw docker argv.
func TestSetupDockerIsolationCidfileInsertionWithAbsolutePath(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	cmd, args, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)
	require.False(t, shellWrapped)
	require.Equal(t, fakeDocker, cmd)

	withCid := c.insertCidfileIntoDockerArgs(args, "/tmp/cid.txt")
	require.GreaterOrEqual(t, len(withCid), 3)
	assert.Equal(t, []string{cmdRun, "--cidfile", "/tmp/cid.txt"}, withCid[:3],
		"cidfile must be inserted right after the 'run' token, got: %v", withCid)
}

// TestInsertCidfileIntoDockerArgs unit-tests the args-based cidfile helper used
// on the direct-exec spawn path. It is platform-agnostic (operates on argv),
// sidestepping the -c vs /c shell quirk the shell-wrapped path had to handle.
func TestInsertCidfileIntoDockerArgs(t *testing.T) {
	c := newIsolatedTestClient()

	t.Run("inserts after run", func(t *testing.T) {
		args := []string{"run", "-i", "--rm", "mcp/duckduckgo"}
		got := c.insertCidfileIntoDockerArgs(args, "/tmp/cid.txt")
		assert.Equal(t, []string{"run", "--cidfile", "/tmp/cid.txt", "-i", "--rm", "mcp/duckduckgo"}, got)
	})

	t.Run("no-op when first arg is not run", func(t *testing.T) {
		args := []string{"version"}
		got := c.insertCidfileIntoDockerArgs(args, "/tmp/cid.txt")
		assert.Equal(t, args, got, "must not mutate args that don't start with 'run'")
	})

	t.Run("no-op on empty args", func(t *testing.T) {
		got := c.insertCidfileIntoDockerArgs(nil, "/tmp/cid.txt")
		assert.Empty(t, got)
	})
}

// TestInsertCidfileWindowsCmdFormat verifies cidfile insertion works with the
// Windows cmd.exe shell-wrap format: ["/c", "docker run …"] (second-to-last
// arg is "/c", not "-c"). This is the shell-fallback path's helper.
func TestInsertCidfileWindowsCmdFormat(t *testing.T) {
	c := newIsolatedTestClient()
	// Simulate Windows cmd.exe output: shell returns ["/c", cmdStr]
	windowsShellArgs := []string{"/c", "docker run --rm -i ghcr.io/some/image python -m srv"}
	withCid := c.insertCidfileIntoShellDockerCommand(windowsShellArgs, "/tmp/cid.txt")
	cmdStr := withCid[len(withCid)-1]
	assert.Contains(t, cmdStr, "docker run --cidfile /tmp/cid.txt",
		"cidfile must be inserted in Windows cmd /c format too, got: %s", cmdStr)
}

// TestSetupDockerIsolationFallsBackToBareDocker verifies that when docker
// cannot be resolved to an absolute path, the spawn falls back to a login-shell
// wrap of the bare "docker" command (preserving prior behavior / login-shell
// PATH resolution as a last resort).
func TestSetupDockerIsolationFallsBackToBareDocker(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) {
		return "", fmt.Errorf("docker not found")
	}

	c := newIsolatedTestClient()
	_, shellArgs, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped, "on resolution failure the spawn must be shell-wrapped")
	require.NotEmpty(t, shellArgs)
	cmdStr := shellArgs[len(shellArgs)-1]
	assert.True(t, strings.HasPrefix(cmdStr, "docker run"),
		"on resolution failure the spawn must fall back to bare 'docker', got: %s", cmdStr)
}
