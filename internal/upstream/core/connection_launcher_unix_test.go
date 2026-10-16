//go:build unix

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/launcher"
)

// spawnBackgroundingLauncher starts a launcher that backgrounds a TERM-immune
// long-lived descendant with redirected streams and exits (issue #1614).
func spawnBackgroundingLauncher(t *testing.T) (launcher.Handle, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`(trap '' TERM; exec sleep 300) </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := launcher.Spawn(context.Background(), &launcher.Spec{Cmd: cmd, Name: "bg-1614", StopGrace: 200 * time.Millisecond}, zap.NewNop())
	require.NoError(t, err)
	var pid int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return h, pid
}

func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	st := strings.TrimSpace(string(out))
	return st != "" && !strings.HasPrefix(st, "Z")
}

// stopLauncher must take down a descendant left behind when the launcher
// leader exited first, even when the Stop context is already expired.
func TestStopLauncher_LeaderExitedFirst_KillsDescendant(t *testing.T) {
	h, pid := spawnBackgroundingLauncher(t)
	require.True(t, alive(pid), "control: descendant runs")

	c := newStrategyTestClient()
	c.logger = zap.NewNop()
	c.launcherHandle = h

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.stopLauncher(ctx)

	require.Eventually(t, func() bool { return !alive(pid) },
		5*time.Second, 20*time.Millisecond, "descendant must not outlive launcher teardown")
}
