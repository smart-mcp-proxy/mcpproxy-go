//go:build windows

package winjob

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

func TestHoldPIDKeepsAReapedProcessPID(t *testing.T) {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "exit 0")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	release := HoldPID(pid)
	require.NoError(t, cmd.Wait()) // reaps it and closes Go's own handle

	require.True(t, exitedProcessAt(pid), "the held PID still names the exited process")
	release()
	require.False(t, exitedProcessAt(pid), "after release nothing keeps the PID")
}
