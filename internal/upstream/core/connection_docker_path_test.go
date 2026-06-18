package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSetupDockerIsolationReturnsBundleDirOnDirectExec is the root-cause
// assertion for #715 / MCP-2877. When docker resolves to a verified absolute
// bundle-dir binary and is direct-exec'd, setupDockerIsolation must report that
// binary's directory so the caller can prepend it to the child PATH. Without
// the bundle dir on PATH, docker cannot exec its sibling credential helper
// (docker-credential-desktop, named by ~/.docker/config.json's credsStore) and
// any registry pull of an uncached isolation image fails.
func TestSetupDockerIsolationReturnsBundleDirOnDirectExec(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	cmd, _, shellWrapped, dockerDir := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.False(t, shellWrapped)
	require.Equal(t, fakeDocker, cmd)
	assert.Equal(t, filepath.Dir(fakeDocker), dockerDir,
		"setupDockerIsolation must report the resolved docker binary's bundle dir for PATH augmentation")
}

// TestSetupDockerIsolationReturnsBundleDirOnShellWrapFallback verifies the dir
// is reported even when we keep the login-shell wrap (non-Darwin, no DOCKER_HOST
// in env): the issue specifies seeding the env PATH is harmless on the shell-wrap
// path and helps if the login shell preserves the inherited PATH. The gate is
// "resolved to a verified absolute executable", not "direct-exec'd".
func TestSetupDockerIsolationReturnsBundleDirOnShellWrapFallback(t *testing.T) {
	fakeDocker := writeFakeDockerExecutable(t)
	forceDockerDaemonEnvGOOS(t, "linux")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	c := newIsolatedTestClient()
	_, _, shellWrapped, dockerDir := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped, "non-Darwin without DOCKER_HOST keeps the shell wrap")
	assert.Equal(t, filepath.Dir(fakeDocker), dockerDir,
		"a verified absolute resolved path must report its bundle dir even on the shell-wrap fallback")
}

// TestSetupDockerIsolationNoBundleDirWhenUnresolved verifies no dir is reported
// when docker did not resolve to a verified absolute path (bare-'docker'
// fallback) — there is no filepath.Dir to take, and augmenting PATH would be
// meaningless / unsafe.
func TestSetupDockerIsolationNoBundleDirWhenUnresolved(t *testing.T) {
	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })
	resolveDockerBinary = func(_ *zap.Logger) (string, error) { return "docker", nil }

	c := newIsolatedTestClient()
	_, _, shellWrapped, dockerDir := c.setupDockerIsolation(c.config.Command, c.config.Args)

	require.True(t, shellWrapped)
	assert.Empty(t, dockerDir, "a non-absolute resolved value must not report a bundle dir")
}

// TestPrependDockerDirToPath unit-tests the pure PATH-augmentation helper.
func TestPrependDockerDirToPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	dir := filepath.FromSlash("/Applications/Docker.app/Contents/Resources/bin")

	t.Run("prepends to existing PATH preserving entries", func(t *testing.T) {
		env := []string{"FOO=bar", "PATH=/usr/bin" + sep + "/bin"}
		got := prependDockerDirToPath(env, dir)
		var path string
		for _, kv := range got {
			if strings.HasPrefix(kv, "PATH=") {
				path = strings.TrimPrefix(kv, "PATH=")
			}
		}
		parts := filepath.SplitList(path)
		require.NotEmpty(t, parts)
		assert.Equal(t, dir, parts[0], "bundle dir must be prepended (front), got: %s", path)
		assert.Contains(t, parts, "/usr/bin", "existing entries preserved")
		assert.Contains(t, parts, "/bin", "existing entries preserved")
		assert.Contains(t, got, "FOO=bar", "unrelated env vars untouched")
	})

	t.Run("dedup: already present leaves PATH unchanged", func(t *testing.T) {
		env := []string{"PATH=/usr/bin" + sep + dir + sep + "/bin"}
		got := prependDockerDirToPath(env, dir)
		assert.Equal(t, env, got, "must not re-add a dir already on PATH")
	})

	t.Run("empty dir is a no-op", func(t *testing.T) {
		env := []string{"PATH=/usr/bin"}
		got := prependDockerDirToPath(env, "")
		assert.Equal(t, env, got)
	})

	t.Run("adds PATH when none present", func(t *testing.T) {
		env := []string{"FOO=bar"}
		got := prependDockerDirToPath(env, dir)
		assert.Contains(t, got, "PATH="+dir)
		assert.Contains(t, got, "FOO=bar")
	})

	t.Run("empty PATH value yields just the dir", func(t *testing.T) {
		env := []string{"PATH="}
		got := prependDockerDirToPath(env, dir)
		assert.Contains(t, got, "PATH="+dir)
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		env := []string{"PATH=/usr/bin"}
		_ = prependDockerDirToPath(env, dir)
		assert.Equal(t, "PATH=/usr/bin", env[0], "input slice must not be mutated")
	})
}
