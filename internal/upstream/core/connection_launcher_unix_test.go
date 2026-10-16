//go:build unix

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// giveUpHandle simulates Stop giving up (expired context) without taking the
// group down; Kill is counted and delegated so the test cleans up.
type giveUpHandle struct {
	launcher.Handle
	kills atomic.Int32
}

func (h *giveUpHandle) Stop(context.Context) error { return context.DeadlineExceeded }
func (h *giveUpHandle) Kill() error {
	h.kills.Add(1)
	return h.Handle.Kill()
}

// When Stop gives up, stopLauncher must hard-kill the group (#1614).
func TestStopLauncher_StopGivesUp_KillsGroup(t *testing.T) {
	h, pid := spawnBackgroundingLauncher(t)
	fake := &giveUpHandle{Handle: h}

	c := newStrategyTestClient()
	c.logger = zap.NewNop()
	c.launcherHandle = fake

	c.stopLauncher(context.Background())

	require.Equal(t, int32(1), fake.kills.Load(), "stopLauncher must Kill after Stop fails")
	require.Eventually(t, func() bool { return !alive(pid) },
		5*time.Second, 20*time.Millisecond, "descendant must not outlive launcher teardown")
}

// The wait-for-URL failure path must hard-kill the group when Stop gives up.
func TestConnectWithLauncher_WaitForURLFailure_StopGivesUp_Kills(t *testing.T) {
	fake := &giveUpHandle{}
	prev := launcherHandleWrapHook
	launcherHandleWrapHook = func(h launcher.Handle) launcher.Handle {
		fake.Handle = h
		return fake
	}
	defer func() { launcherHandleWrapHook = prev }()

	c := newRetireCoreClient(t, &config.ServerConfig{
		Name:                "launcher-waiturl-kill",
		URL:                 "http://" + closedPort(t) + "/mcp",
		Protocol:            "http",
		Command:             "/bin/sh",
		Args:                []string{"-c", "exec sleep 300"},
		LauncherWaitTimeout: config.Duration(300 * time.Millisecond),
		Enabled:             true,
	}, nil)

	c.mu.Lock()
	err := c.connectWithLauncher(context.Background())
	c.mu.Unlock()
	require.Error(t, err)
	require.Equal(t, int32(1), fake.kills.Load(), "wait-for-URL failure must Kill after Stop fails")
	require.Eventually(t, func() bool { return !alive(fake.Pid()) || fake.Pid() == 0 },
		5*time.Second, 20*time.Millisecond)
}
