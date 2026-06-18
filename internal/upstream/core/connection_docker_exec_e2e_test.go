package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRecordingDockerShim writes a fake `docker` shell script that appends its
// own argv[0] (the path it was invoked as) to recordFile and exits 0. It lets a
// test prove, at the OS-exec level, HOW docker was launched: a direct-exec
// records the absolute bundle path, while a bare-`docker` login-shell wrap would
// either fail (`command not found`) or record just "docker".
func writeRecordingDockerShim(t *testing.T, dir, recordFile string) string {
	t.Helper()
	name := "docker"
	if runtime.GOOS == osWindows {
		name = "docker.bat"
	}
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> " + shellSingleQuote(recordFile) + "\nexit 0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording docker shim: %v", err)
	}
	return p
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TestE2E_DockerSpawnExecsAbsoluteBundlePath is the behavioral end-to-end proof
// for #696. It drives the REAL shellwrap.ResolveDockerPath + setupDockerIsolation
// chain under the field scenario (Docker Desktop bundle present, docker absent
// from the spawn PATH and login shell), then ACTUALLY execs the resulting
// (command, args) the way connection_stdio would. A recording shim captures the
// path it was invoked as.
//
// The assertion is the crux of the bug report: the process is launched by its
// ABSOLUTE bundle path — so it CANNOT produce `zsh:1: command not found: docker`
// — proving the spawn pipeline (not just setupDockerIsolation's return value)
// uses the resolved binary.
func TestE2E_DockerSpawnExecsAbsoluteBundlePath(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("shim is a /bin/sh script; unix-only")
	}
	useRealDockerResolver(t)
	forceDockerDaemonEnvGOOS(t, osDarwin)

	bundleDir := t.TempDir()
	recordFile := filepath.Join(t.TempDir(), "invocations.log")
	bundleDocker := writeRecordingDockerShim(t, bundleDir, recordFile)

	restore := shellwrap.SetWellKnownDockerPathsForTest(func() []string { return []string{bundleDocker} })
	t.Cleanup(restore)
	// Spawn PATH and login shell cannot resolve docker — only the well-known
	// bundle probe can (the exact #696 condition).
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/nonexistent/shell-must-not-be-invoked")

	c := newIsolatedTestClient()
	cmd, args, shellWrapped := c.setupDockerIsolation(c.config.Command, c.config.Args)
	require.False(t, shellWrapped, "#696: bundle-resolved docker must be direct-exec'd")

	// Exec it exactly as connection_stdio would (exec.CommandContext(cmd, args...)).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execCmd := exec.CommandContext(ctx, cmd, args...)
	require.NoError(t, execCmd.Run(), "spawning the resolved docker binary must succeed")

	recorded, err := os.ReadFile(recordFile)
	require.NoError(t, err, "the docker shim must have been invoked and recorded its argv[0]")
	got := strings.TrimSpace(string(recorded))

	assert.Equal(t, bundleDocker, got,
		"#696: docker must be exec'd by its absolute bundle path, got %q", got)
	assert.NotEqual(t, "docker", got, "#696: must not be launched as bare 'docker'")
	require.True(t, filepath.IsAbs(got), "argv[0] must be absolute, got %q", got)
}
