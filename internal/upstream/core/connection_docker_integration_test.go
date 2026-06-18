package core

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// newObservedIsolatedClient returns an isolated test client whose logger feeds
// an in-memory observer, so tests can assert on the spawn-decision INFO logs.
func newObservedIsolatedClient(t *testing.T) (*Client, *observer.ObservedLogs) {
	t.Helper()
	core, recorded := observer.New(zap.InfoLevel)
	c := newIsolatedTestClient()
	c.logger = zap.New(core)
	return c, recorded
}

// TestSpawnDecisionIsObservable_DirectExec asserts the #696 diagnosability fix:
// the direct-exec branch emits an INFO line carrying the resolved docker_path so
// a field report can be triaged from main.log alone.
func TestSpawnDecisionIsObservable_DirectExec(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix fake-binary fixture")
	}
	useRealDockerResolver(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	bundleDocker := writeFakeDockerExecutable(t)
	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return []string{bundleDocker} })
	t.Cleanup(restore)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	c, recorded := newObservedIsolatedClient(t)
	_, _, shellWrapped, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)
	require.False(t, shellWrapped)

	entries := recorded.FilterMessage("Docker spawn: direct-exec of resolved docker binary").All()
	require.Len(t, entries, 1, "direct-exec must log exactly one observable spawn-decision line")
	fields := entries[0].ContextMap()
	assert.Equal(t, bundleDocker, fields["docker_path"], "spawn log must record the resolved absolute docker path")
	assert.Equal(t, false, fields["shell_wrapped"])
}

// TestSpawnDecisionIsObservable_ShellWrapFallback asserts the fallback branch is
// equally observable and flags whether docker even resolved — the one path that
// can yield #696's `command not found: docker`.
func TestSpawnDecisionIsObservable_ShellWrapFallback(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) {
		return "", fmt.Errorf("docker not found")
	}

	c, recorded := newObservedIsolatedClient(t)
	_, _, shellWrapped, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)
	require.True(t, shellWrapped)

	entries := recorded.FilterMessage("Docker spawn: login-shell wrap fallback").All()
	require.Len(t, entries, 1, "shell-wrap fallback must log exactly one observable spawn-decision line")
	fields := entries[0].ContextMap()
	assert.Equal(t, true, fields["shell_wrapped"])
	assert.Equal(t, false, fields["docker_resolved"], "unresolved docker must be flagged in the spawn log")
	assert.Equal(t, cmdDocker, fields["docker_command"], "fallback with no resolution wraps bare 'docker'")
}

// These tests close the integration gap behind GitHub #696: the existing
// connection_docker_test.go suite STUBS resolveDockerBinary, so it proves the
// gating logic in isolation but never exercises the REAL
// shellwrap.ResolveDockerPath -> setupDockerIsolation chain. The #696 field
// failure ("docker installed via Docker Desktop but not on the spawn PATH"
// still spawns bare `docker` and dies with `command not found: docker`) can
// only be reproduced — or refuted — by driving the real resolver end-to-end.
//
// useRealDockerResolver pins resolveDockerBinary to the production resolver
// (un-stubbing any leakage from a sibling test) and clears the process-wide
// docker-path cache so resolution starts from a clean slate.
func useRealDockerResolver(t *testing.T) {
	t.Helper()
	orig := resolveDockerBinary
	resolveDockerBinary = shellwrap.ResolveDockerPath
	shellwrap.ResetDockerPathCacheForTest()
	t.Cleanup(func() {
		resolveDockerBinary = orig
		shellwrap.ResetDockerPathCacheForTest()
	})
}

// TestIntegration_DockerOnlyAtBundlePath_DirectExecs is the faithful #696
// reproduction: on macOS, Docker Desktop's CLI lives only inside the app bundle
// (/Applications/Docker.app/Contents/Resources/bin/docker) and is absent from
// the spawn PATH. The real resolver must discover it via the well-known-path
// probe, and setupDockerIsolation must DIRECT-EXEC that absolute path — never
// fall back to a `$SHELL -l -c "docker run …"` wrap with bare `docker` (which is
// what the login shell cannot resolve and what the field report shows failing).
func TestIntegration_DockerOnlyAtBundlePath_DirectExecs(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("well-known bundle probe + login-shell fallback are unix-only")
	}
	useRealDockerResolver(t)
	forceDockerDaemonEnvGOOS(t, osDarwin) // matches the affected hosts

	// Docker exists ONLY at a "bundle" path, off the spawn PATH.
	bundleDocker := writeFakeDockerExecutable(t)
	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return []string{bundleDocker} })
	t.Cleanup(restore)

	// Spawn PATH excludes the bundle dir (the #696 condition) and the login
	// shell can't find docker either, so the well-known probe is the ONLY way to
	// resolve it.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	c := newIsolatedTestClient()
	cmd, args, shellWrapped, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.False(t, shellWrapped,
		"#696: docker resolved at the bundle path MUST be direct-exec'd, not shell-wrapped with bare docker")
	assert.Equal(t, bundleDocker, cmd,
		"#696: spawn must exec the resolved absolute bundle path, got %q", cmd)
	assert.NotEqual(t, cmdDocker, cmd, "#696: spawn must not degrade to bare 'docker'")
	require.True(t, filepath.IsAbs(cmd), "spawn command must be absolute, got %q", cmd)
	require.NotEmpty(t, args)
	assert.Equal(t, cmdRun, args[0], "args must be raw argv (first token 'run'), got %v", args)
	for _, a := range args {
		assert.NotContains(t, a, " run ", "args must be raw argv tokens, not a shell -c string: %v", args)
	}
}

// TestIntegration_DockerOnPath_DirectExecs covers the other real resolution
// leg: when docker IS on the spawn PATH, exec.LookPath resolves it to an
// absolute path and the spawn direct-execs it.
func TestIntegration_DockerOnPath_DirectExecs(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix fake-binary fixture")
	}
	useRealDockerResolver(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	pathDocker := writeFakeDockerExecutable(t)
	// Put the fake docker's dir on PATH so exec.LookPath finds it first.
	t.Setenv("PATH", filepath.Dir(pathDocker))
	// No well-known override needed; LookPath should win before the probe.
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	c := newIsolatedTestClient()
	cmd, args, shellWrapped, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.False(t, shellWrapped, "docker on PATH must resolve to an absolute path and direct-exec")
	assert.Equal(t, pathDocker, cmd, "spawn must exec the LookPath-resolved absolute path, got %q", cmd)
	require.NotEmpty(t, args)
	assert.Equal(t, cmdRun, args[0])
}

// TestIntegration_DockerUnresolvable_FallsBackToBareDocker proves the worst
// case (#696 absent): when docker is nowhere — not on PATH, not at a well-known
// path, not in the login shell — the spawn falls back to a login-shell wrap of
// bare `docker`. This is the ONLY path that should ever produce the field
// failure's `command not found: docker`, and it requires docker to be genuinely
// absent (not merely off PATH).
func TestIntegration_DockerUnresolvable_FallsBackToBareDocker(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix login-shell fallback")
	}
	useRealDockerResolver(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	// No docker anywhere: empty PATH, well-known list points at nothing, login
	// shell sabotaged so its lookup fails too.
	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return nil })
	t.Cleanup(restore)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	c := newIsolatedTestClient()
	_, shellArgs, shellWrapped, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped, "unresolvable docker must fall back to the login-shell wrap")
	require.NotEmpty(t, shellArgs)
	cmdStr := shellArgs[len(shellArgs)-1]
	assert.True(t, strings.HasPrefix(cmdStr, "docker run"),
		"only a genuinely-absent docker should degrade to bare 'docker', got %q", cmdStr)
}

// TestIntegration_StatusAndSpawnResolverDoNotDiverge guards the invariant the
// #696 v0.39.1 report worried about ("detection discovers docker_path but the
// spawn doesn't use it"). docker_status.docker_path (server.dockerPathResolver)
// and the spawn (core.resolveDockerBinary) both call shellwrap.ResolveDockerPath
// and share ONE process-wide cache, so for a given environment they MUST agree.
// If a future refactor gives the status panel its own resolver, this test
// fails — surfacing exactly the divergence the field reports feared.
func TestIntegration_StatusAndSpawnResolverDoNotDiverge(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix fake-binary fixture")
	}
	useRealDockerResolver(t)

	bundleDocker := writeFakeDockerExecutable(t)
	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return []string{bundleDocker} })
	t.Cleanup(restore)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	// Spawn side (the value setupDockerIsolation would exec).
	spawnPath, err := resolveDockerBinary(zap.NewNop())
	require.NoError(t, err)

	// Status side reports the SAME resolution branch + path from the shared cache.
	source := shellwrap.ResolveDockerSource(zap.NewNop())
	statusPath, err := shellwrap.ResolveDockerPath(zap.NewNop())
	require.NoError(t, err)

	assert.Equal(t, bundleDocker, spawnPath, "spawn must resolve the bundle path")
	assert.Equal(t, spawnPath, statusPath, "status and spawn must resolve to the SAME docker path (no divergence)")
	assert.Equal(t, shellwrap.DockerSourceBundled, source,
		"status must report the bundled source matching the spawn resolution")
	assert.True(t, isDirectExecutable(statusPath),
		"the path docker_status reports must itself satisfy the direct-exec contract")
}
