//go:build windows

package core

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// pingTree mirrors internal/winjob's test helper: a cmd.exe -> ping.exe
// tree where cmd.exe blocks on `set /p` until release() writes a line to
// stdin (so the Job is attached BEFORE the grandchild exists), and where
// ping.exe inherits the stdout pipe so EOF == "the whole tree is gone".
type pingTree struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pinging <-chan struct{}
	eof     <-chan struct{}
}

func startPingTree(t *testing.T) *pingTree {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "set /p x=& ping -n 30 127.0.0.1")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pinging := make(chan struct{})
	eof := make(chan struct{})
	go func() {
		defer close(eof)
		seen := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			// Any ping output line names the target; locale-neutral.
			if !seen && strings.Contains(sc.Text(), "127.0.0.1") {
				seen = true
				close(pinging)
			}
		}
	}()
	return &pingTree{cmd: cmd, stdin: stdin, pinging: pinging, eof: eof}
}

func (p *pingTree) release(t *testing.T) {
	t.Helper()
	_, err := io.WriteString(p.stdin, "\r\n")
	require.NoError(t, err)
	select {
	case <-p.pinging:
	case <-time.After(10 * time.Second):
		t.Fatal("ping.exe never produced output; grandchild not running")
	}
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
	tree := startPingTree(t)

	pgid := extractProcessGroupID(tree.cmd, logger, "test-server")
	require.Equal(t, tree.cmd.Process.Pid, pgid)
	require.True(t, hasWindowsJob(pgid), "extractProcessGroupID must register the Job")
	tree.release(t)

	// Simulate what mcp-go's Stdio.Close() does on Windows: kill cmd.exe only.
	require.NoError(t, tree.cmd.Process.Kill())
	select {
	case <-tree.eof:
		t.Fatal("grandchild did not inherit the pipe; test cannot prove anything")
	case <-time.After(1500 * time.Millisecond):
	}

	releaseProcessGroup(pgid, tree.cmd, logger, "test-server")

	assert.False(t, hasWindowsJob(pgid), "releaseProcessGroup must drop the windowsJobs entry")
	select {
	case <-tree.eof:
	case <-time.After(5 * time.Second):
		t.Fatal("grandchild survived releaseProcessGroup")
	}
}

// TestKillProcessGroup_ForgetsJob: the pre-existing force-kill path must
// leave no entry behind either, so a later releaseProcessGroup is a no-op.
func TestKillProcessGroup_ForgetsJob(t *testing.T) {
	logger := zap.NewNop()
	tree := startPingTree(t)

	pgid := extractProcessGroupID(tree.cmd, logger, "test-server")
	require.True(t, hasWindowsJob(pgid))
	tree.release(t)

	require.NoError(t, killProcessGroup(pgid, logger, "test-server"))
	assert.False(t, hasWindowsJob(pgid))
	select {
	case <-tree.eof:
	case <-time.After(5 * time.Second):
		t.Fatal("tree survived killProcessGroup")
	}
	releaseProcessGroup(pgid, tree.cmd, logger, "test-server") // must be a harmless no-op
}

// TestReleaseProcessGroup_PIDReuseLeavesNewOwnerAlone: Disconnect captures
// pgid+cmd, then mcp-go's Close() reaps cmd.exe, and only THEN does
// releaseProcessGroup run. In that window Windows can hand the PID to a
// concurrently connecting server. The old owner's release must not close
// the new owner's Job; the takeover itself must close the old one.
func TestReleaseProcessGroup_PIDReuseLeavesNewOwnerAlone(t *testing.T) {
	logger := zap.NewNop()
	const pid = 1 << 30 // never a real PID; the map does not care
	oldCmd, newCmd := exec.Command("cmd.exe"), exec.Command("cmd.exe")

	oldJob, err := winjob.New()
	require.NoError(t, err)
	newJob, err := winjob.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = oldJob.Close(); _ = newJob.Close() })

	registerWindowsJob(pid, oldCmd, oldJob, logger, "old-server")
	registerWindowsJob(pid, newCmd, newJob, logger, "new-server") // PID reuse

	// The takeover closed the old Job: Assign on a closed Job fails
	// without touching Win32 (a live Job with a bogus PID would fail
	// differently, inside OpenProcess).
	err = oldJob.Assign(pid)
	require.ErrorContains(t, err, "already closed")

	// The old owner's delayed release must leave the new owner's entry alone.
	releaseProcessGroup(pid, oldCmd, logger, "old-server")
	require.True(t, hasWindowsJob(pid), "old owner's release must not evict the new owner's Job")

	// The new owner's own release works as usual.
	releaseProcessGroup(pid, newCmd, logger, "new-server")
	assert.False(t, hasWindowsJob(pid))
}
