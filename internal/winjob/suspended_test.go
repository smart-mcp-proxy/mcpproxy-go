//go:build windows

package winjob

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A CREATE_SUSPENDED child runs no code until it is resumed, and once
// assigned-and-resumed everything it spawns is inside the job.
func TestAssignAndResume_ChildRunsOnlyAfterAssignment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.txt")
	cmd := exec.Command("cmd.exe", "/c", "echo ran> \""+marker+"\"")
	MarkSuspended(cmd)
	require.True(t, IsSuspended(cmd))
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	time.Sleep(700 * time.Millisecond)
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err), "a suspended child must not run before it is resumed")

	job, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = job.Close() })
	require.NoError(t, job.AssignAndResume(cmd.Process.Pid))

	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 10*time.Second, 50*time.Millisecond, "child must run after it is resumed")
}

func TestMarkSuspended_KeepsExistingAttrs(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	require.False(t, IsSuspended(cmd))
	MarkSuspended(cmd)
	MarkSuspended(cmd)
	require.True(t, IsSuspended(cmd))
}

// A job that is already closed refuses the assignment, so the caller kills
// the still-suspended child instead of resuming it.
func TestAssignAndResume_ClosedJobLeavesChildSuspended(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.txt")
	cmd := exec.Command("cmd.exe", "/c", "echo ran> \""+marker+"\"")
	MarkSuspended(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	job, err := New()
	require.NoError(t, err)
	require.NoError(t, job.Close())
	require.Error(t, job.AssignAndResume(cmd.Process.Pid))

	time.Sleep(700 * time.Millisecond)
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err), "a failed assignment must not resume the child")
}
