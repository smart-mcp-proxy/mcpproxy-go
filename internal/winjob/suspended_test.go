//go:build windows

package winjob

import (
	"io"
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
		_, _ = os.Stdout.WriteString(childReady)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "requester":
		os.Exit(requestChildExit())
	}
	os.Exit(m.Run())
}

// breakHelperEnv makes the test binary act as a process that sends CTRL_BREAK
// to its own console group ("parent"), or as a child it started ("child").
const breakHelperEnv = "MCPPROXY_WINJOB_BREAK"

const childReady = "ready"

// startReady starts cmd and waits until it runs its own code.
func startReady(cmd *exec.Cmd, afterStart func() error) (exited <-chan struct{}, ok bool) {
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil || (afterStart != nil && afterStart() != nil) {
		return nil, false
	}
	stuck := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() }) // ends the read below
	defer stuck.Stop()
	line := make([]byte, len(childReady))
	if _, err := io.ReadFull(out, line); err != nil || string(line) != childReady {
		return nil, false
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	return done, true
}

func breakChild() *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	cmd.Env = append(os.Environ(), breakHelperEnv+"=child")
	return cmd
}

// breakParent starts a child the way the core starts its stdio upstreams and a control child in its
// own group, sends CTRL_BREAK to that group and returns 0 when it ends only the control child.
func breakParent() int {
	got := make(chan os.Signal, 1)
	signal.Notify(got, os.Interrupt)
	job, err := New()
	if err != nil {
		return 3
	}
	child, control := breakChild(), breakChild()
	MarkSuspended(child)
	childExited, ok := startReady(child, func() error { return job.AssignAndResume(child.Process.Pid) })
	if !ok {
		return 3
	}
	controlExited, ok := startReady(control, nil)
	if !ok {
		return 3
	}
	if windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(os.Getpid())) != nil {
		return 4
	}
	for _, c := range []<-chan struct{}{controlExited, asDone(got)} {
		select {
		case <-c:
		case <-time.After(10 * time.Second):
			return 5
		}
	}
	// The same event ended the control child, so it would have reached this one by now.
	select {
	case <-childExited:
		return 6
	case <-time.After(500 * time.Millisecond):
	}
	_ = job.Close() // still contained: closing the job ends it
	select {
	case <-childExited:
		return 0
	case <-time.After(10 * time.Second):
		return 7
	}
}

func asDone(sig <-chan os.Signal) <-chan struct{} {
	done := make(chan struct{})
	go func() { <-sig; close(done) }()
	return done
}

// requestChildExit starts a child that never exits by itself, asks it to with RequestExit and
// returns 0 when only the child ends.
func requestChildExit() int {
	got := make(chan os.Signal, 1)
	signal.Notify(got, os.Interrupt)
	job, err := New()
	if err != nil {
		return 3
	}
	defer job.Close()
	child := breakChild()
	MarkSuspended(child)
	exited, ok := startReady(child, func() error { return job.AssignAndResume(child.Process.Pid) })
	if !ok {
		return 3
	}
	if RequestExit(child.Process.Pid) != nil {
		return 4
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		return 5
	}
	select {
	case <-got:
		return 6
	case <-time.After(300 * time.Millisecond):
		return 0
	}
}

// Windows has no SIGTERM, so this is the second step of the MCP stdio shutdown there.
func TestRequestExit_EndsOnlyTheChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	cmd.Env = append(os.Environ(), breakHelperEnv+"=requester")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "exit codes: 3 setup, 4 send, 5 child stayed, 6 parent got the signal; output: %s", out)
}

// The tray stops the core with CTRL_BREAK. Upstream children must not get it, or
// every call running on them fails at once instead of getting the shutdown grace.
func TestMarkSuspended_ChildDoesNotGetTheParentsCtrlBreak(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-executes the test binary
	cmd.Env = append(os.Environ(), breakHelperEnv+"=parent")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "exit codes: 3 setup, 4 send, 5 control child or parent got no signal, 6 child died, 7 child survived its job; output: %s", out)
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
