//go:build windows

package core

import (
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// startPingTree mirrors internal/winjob's helper: cmd.exe -> ping.exe, where
// ping.exe inherits the stdout pipe so EOF == "the whole tree is gone".
func startPingTree(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	eof := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(eof)
	}()
	return cmd, eof
}

func hasWindowsJob(pid int) bool {
	windowsJobsMu.Lock()
	defer windowsJobsMu.Unlock()
	_, ok := windowsJobs[pid]
	return ok
}

// TestReleaseProcessGroup_KillsTreeAndForgetsJob covers the normal
// Disconnect path (F1 of the #1234 review): mcp-go's Close() only ever
// reaches the immediate child, so releaseProcessGroup must both terminate
// the surviving grandchildren AND drop the windowsJobs entry, or the Job
// handle and the tree live until mcpproxy exits.
func TestReleaseProcessGroup_KillsTreeAndForgetsJob(t *testing.T) {
	logger := zap.NewNop()
	cmd, eof := startPingTree(t)

	pgid := extractProcessGroupID(cmd, logger, "test-server")
	require.Equal(t, cmd.Process.Pid, pgid)
	require.True(t, hasWindowsJob(pgid), "extractProcessGroupID must register the Job")

	// Simulate what mcp-go's Stdio.Close() does on Windows: kill cmd.exe only.
	require.NoError(t, cmd.Process.Kill())
	select {
	case <-eof:
		t.Fatal("grandchild did not inherit the pipe; test cannot prove anything")
	case <-time.After(1500 * time.Millisecond):
	}

	releaseProcessGroup(pgid, logger, "test-server")

	assert.False(t, hasWindowsJob(pgid), "releaseProcessGroup must drop the windowsJobs entry")
	select {
	case <-eof:
	case <-time.After(5 * time.Second):
		t.Fatal("grandchild survived releaseProcessGroup")
	}
}

// TestKillProcessGroup_ForgetsJob: the pre-existing force-kill path must
// leave no entry behind either, so a later releaseProcessGroup is a no-op.
func TestKillProcessGroup_ForgetsJob(t *testing.T) {
	logger := zap.NewNop()
	cmd, eof := startPingTree(t)

	pgid := extractProcessGroupID(cmd, logger, "test-server")
	require.True(t, hasWindowsJob(pgid))

	require.NoError(t, killProcessGroup(pgid, logger, "test-server"))
	assert.False(t, hasWindowsJob(pgid))
	select {
	case <-eof:
	case <-time.After(5 * time.Second):
		t.Fatal("tree survived killProcessGroup")
	}
	releaseProcessGroup(pgid, logger, "test-server") // must be a harmless no-op
}
