package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestResolveDockerStatusUnresolvableReportsUnavailable verifies the honesty
// fix (#696): when the docker CLI cannot be resolved to an absolute path,
// docker is reported unavailable with an empty path — NOT available via a bare
// "docker" probe that is not the binary used for spawning.
func TestResolveDockerStatusUnresolvableReportsUnavailable(t *testing.T) {
	orig := dockerPathResolver
	t.Cleanup(func() { dockerPathResolver = orig })
	dockerPathResolver = func(_ *zap.Logger) (string, error) {
		return "", assert.AnError
	}

	p := &MCPProxyServer{logger: zap.NewNop()}
	available, path := p.resolveDockerStatus()

	assert.False(t, available, "docker must be reported unavailable when unresolvable")
	assert.Equal(t, "", path, "docker_path must be empty when unresolvable")
}

// TestResolveDockerStatusResolvableAndWorking verifies that a resolvable docker
// whose `docker info` exits 0 is reported available with its resolved path.
func TestResolveDockerStatusResolvableAndWorking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell-script fake docker; not portable to Windows")
	}

	// A fake "docker" that exits 0 for any args (including `info`).
	dir := t.TempDir()
	fakeDocker := filepath.Join(dir, "docker")
	require.NoError(t, os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	orig := dockerPathResolver
	t.Cleanup(func() { dockerPathResolver = orig })
	dockerPathResolver = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	p := &MCPProxyServer{logger: zap.NewNop()}
	available, path := p.resolveDockerStatus()

	assert.True(t, available, "docker must be reported available when resolvable and `info` succeeds")
	assert.Equal(t, fakeDocker, path, "docker_path must surface the resolved binary")
}

// TestResolveDockerStatusResolvableDaemonDown verifies that a resolvable docker
// whose `docker info` fails (daemon down) is reported unavailable but still
// surfaces the resolved path for diagnostics.
func TestResolveDockerStatusResolvableDaemonDown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell-script fake docker; not portable to Windows")
	}

	dir := t.TempDir()
	fakeDocker := filepath.Join(dir, "docker")
	require.NoError(t, os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	orig := dockerPathResolver
	t.Cleanup(func() { dockerPathResolver = orig })
	dockerPathResolver = func(_ *zap.Logger) (string, error) { return fakeDocker, nil }

	p := &MCPProxyServer{logger: zap.NewNop()}
	available, path := p.resolveDockerStatus()

	assert.False(t, available, "docker must be reported unavailable when `info` fails")
	assert.Equal(t, fakeDocker, path, "docker_path must surface the resolved binary even when the daemon is down")
}
