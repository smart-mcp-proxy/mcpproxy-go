//go:build windows

package winjob

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// markerHelperEnv makes the test binary act as a child that writes a marker
// file and exits. Re-executing the test binary avoids cmd.exe quoting rules,
// which do not understand the \" escaping exec.Command applies.
const markerHelperEnv = "MCPPROXY_WINJOB_MARKER"

func TestMain(m *testing.M) {
	if path := os.Getenv(markerHelperEnv); path != "" {
		if err := os.WriteFile(path, []byte("ran"), 0o600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	switch os.Getenv(breakHelperEnv) {
	case "parent":
		os.Exit(breakParent())
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// breakHelperEnv makes the test binary act as a process that sends CTRL_BREAK
// to its own console group ("parent"), or as a child it started ("child").
const breakHelperEnv = "MCPPROXY_WINJOB_BREAK"

// breakParent starts a child the way the core starts its stdio upstreams, sends
// CTRL_BREAK to its own group and returns 0 when the child outlives it.
func breakParent() int {
	got := make(chan os.Signal, 1)
	signal.Notify(got, os.Interrupt)
	child := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	child.Env = append(os.Environ(), breakHelperEnv+"=child")
	MarkSuspended(child)
	job, err := New()
	if err != nil || child.Start() != nil || job.AssignAndResume(child.Process.Pid) != nil {
		return 3
	}
	exited := make(chan struct{})
	go func() { _ = child.Wait(); close(exited) }()
	time.Sleep(700 * time.Millisecond) // let the child start
	if windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(os.Getpid())) != nil {
		return 4
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		return 5
	}
	select {
	case <-exited:
		return 6
	case <-time.After(time.Second):
	}
	_ = job.Close() // still contained: closing the job ends it
	select {
	case <-exited:
		return 0
	case <-time.After(5 * time.Second):
		return 7
	}
}

// The tray stops the core with CTRL_BREAK. Upstream children must not get it, or
// every call running on them fails at once instead of getting the shutdown grace.
func TestMarkSuspended_ChildDoesNotGetTheParentsCtrlBreak(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	cmd.Env = append(os.Environ(), breakHelperEnv+"=parent")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "exit codes: 3 setup, 4 send, 5 parent got no signal, 6 child died, 7 child survived its job; output: %s", out)
}

func markerChild(t *testing.T, marker string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	cmd.Env = append(os.Environ(), markerHelperEnv+"="+marker)
	return cmd
}

// A CREATE_SUSPENDED child runs no code until it is resumed, and once
// assigned-and-resumed everything it spawns is inside the job.
func TestAssignAndResume_ChildRunsOnlyAfterAssignment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.txt")
	cmd := markerChild(t, marker)
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
	cmd := markerChild(t, marker)
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
