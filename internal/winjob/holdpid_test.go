//go:build windows

package winjob

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// exitedProcessAt reports whether pid still names a process that has exited: the reaped one.
func exitedProcessAt(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // test PID
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // test cleanup
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code != uint32(windows.STATUS_PENDING)
}

// waitPIDFreed waits until pid no longer names the exited process. Another process on the
// machine (Defender, conhost, the CI runner) may keep its own handle open for a moment, which
// holds the PID just as HoldPID does; the test is skipped if that never ends.
func waitPIDFreed(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); exitedProcessAt(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Skipf("another process still holds a handle to exited PID %d", pid)
		}
	}
}

func TestHoldPIDKeepsAReapedProcessPID(t *testing.T) {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "exit 0")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	release, held := HoldPID(pid)
	require.True(t, held)
	require.NoError(t, cmd.Wait()) // reaps it and closes Go's own handle

	require.True(t, exitedProcessAt(pid), "the held PID still names the exited process")
	release()
	waitPIDFreed(t, pid) // after release nothing of ours keeps the PID
}

// A process that cannot be opened is not held, so nothing may be sent to its PID.
func TestHoldPIDReportsAProcessItCannotOpen(t *testing.T) {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "exit 0")
	require.NoError(t, cmd.Run()) // reaped: nothing of ours keeps its PID
	waitPIDFreed(t, cmd.Process.Pid)
	release, held := HoldPID(cmd.Process.Pid)
	defer release()
	require.False(t, held)
}
