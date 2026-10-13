//go:build unix

package launcher

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
)

// Sol review round 2 (Spec 116 PR4): a launcher that backgrounds a
// long-lived descendant with redirected streams and exits closes Done (the
// pumps reach EOF and the leader is reaped) while the descendant still runs
// in the launcher's process group. The retirement kill must still reach it.
func TestHandleKill_AfterLeaderExit_KillsDescendant(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "bg-launcher"}, zap.NewNop())
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher leader did not exit")
	}
	pid := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.NoError(t, syscall.Kill(pid, 0), "control: the descendant outlives the leader")

	require.NoError(t, h.Kill())
	// Gone or a zombie: in a container whose PID 1 does not reap orphans
	// the killed descendant stays a zombie, which kill(pid, 0) still finds
	// (review round 3).
	require.Eventually(t, func() bool { return !liveNonZombie(pid) },
		5*time.Second, 20*time.Millisecond,
		"Kill after the leader exited must reach the descendant left in its process group")
}

// A process group ID is handed out again once the group is empty. After the
// leader was reaped and its group is gone, Kill must not signal a group that
// now belongs to an unrelated process which reused the number (review round
// 2). A live process holding the number as its PID proves the reuse: while
// any member of our group lives, the kernel does not allocate that number.
func TestHandleKill_AfterGroupGone_DoesNotSignalReusedID(t *testing.T) {
	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "short"}, zap.NewNop())
	require.NoError(t, err)
	<-h.Done()
	hh := h.(*handle)
	require.Positive(t, hh.pgid, "the group is recorded at spawn")

	// Simulate the reuse: point the recorded group at a live, unrelated
	// group whose leader holds that number as its PID.
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-exited })
	hh.pgid = other.Process.Pid

	require.NoError(t, h.Kill())
	select {
	case <-exited:
		t.Fatal("an unrelated group with a reused ID was signalled")
	case <-time.After(300 * time.Millisecond):
	}
}

// Sol review round 3: Done closes only after the reaper has logged, so a
// reuse check keyed on Done misses the window between the leader being
// reaped and Done closing. Kill in that window must still leave a reused
// group alone.
func TestHandleKill_ReapedBeforeDone_DoesNotSignalReusedID(t *testing.T) {
	inWindow := make(chan *handle, 1)
	releaseReaper := make(chan struct{})
	hook := func(h *handle) {
		if h.name == "reap-window" {
			inWindow <- h
			<-releaseReaper
		}
	}
	prev := afterReapHook.Swap(&hook)
	t.Cleanup(func() { afterReapHook.Store(prev) })

	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "reap-window"}, zap.NewNop())
	require.NoError(t, err)
	var hh *handle
	select {
	case hh = <-inWindow:
	case <-time.After(10 * time.Second):
		t.Fatal("the reaper did not reach the window")
	}
	defer close(releaseReaper)
	select {
	case <-h.Done():
		t.Fatal("control: Done must still be open in the window")
	default:
	}

	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-exited })
	hh.pgid = other.Process.Pid

	require.NoError(t, h.Kill())
	select {
	case <-exited:
		t.Fatal("an unrelated group with a reused ID was signalled between the reap and Done")
	case <-time.After(300 * time.Millisecond):
	}
}

// Sol review round 4: cmd.Wait released the leader (and, with the group
// empty, the group ID) before reap took reapMu, so a Kill in between saw
// reaped == false and signalled the number unchecked. The reap now runs
// under reapMu: a Kill that arrives after the real reap but before reaped is
// published waits for it and then leaves a reused group alone.
func TestHandleKill_AfterRealReapBeforePublish_DoesNotSignalReusedID(t *testing.T) {
	inWindow := make(chan *handle, 1)
	releaseReaper := make(chan struct{})
	hook := func(h *handle) {
		if h.name == "reap-publish-window" {
			inWindow <- h
			<-releaseReaper
		}
	}
	prev := reapedLockedHook.Swap(&hook)
	t.Cleanup(func() { reapedLockedHook.Store(prev) })

	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "reap-publish-window"}, zap.NewNop())
	require.NoError(t, err)
	var hh *handle
	select {
	case hh = <-inWindow:
	case <-time.After(10 * time.Second):
		t.Fatal("the reaper did not reach the window after the real reap")
	}
	released := false
	release := func() {
		if !released {
			released = true
			close(releaseReaper)
		}
	}
	defer release()
	require.ErrorIs(t, syscall.Kill(hh.cmd.Process.Pid, 0), syscall.ESRCH,
		"control: the leader is really reaped in the window")

	// Simulate the reuse: a live, unrelated group now holds the number.
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-exited })
	hh.pgid = other.Process.Pid

	killed := make(chan error, 1)
	go func() { killed <- h.Kill() }()
	select {
	case <-exited:
		t.Fatal("an unrelated group with a reused ID was signalled between the real reap and publishing reaped")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case err := <-killed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Kill did not return after the reaper published reaped")
	}
	select {
	case <-exited:
		t.Fatal("an unrelated group with a reused ID was signalled after the reap was published")
	case <-time.After(300 * time.Millisecond):
	}
}

// liveNonZombie reports whether pid names a process that is alive and not a
// zombie.
func liveNonZombie(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// pid (comm) S ...: the state follows the last ')'.
		s := string(b)
		if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
			return s[i+2] != 'Z'
		}
		return true
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	st := strings.TrimSpace(string(out))
	return st != "" && !strings.HasPrefix(st, "Z")
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	var raw []byte
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(path)
		raw = b
		return err == nil && strings.TrimSpace(string(b)) != ""
	}, 5*time.Second, 10*time.Millisecond)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	return pid
}
