//go:build unix

package launcher

import (
	"context"
	"errors"
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
// long-lived descendant with redirected streams and exits leaves the
// descendant running in its process group, and the retirement kill must
// reach it. Since review round 6 the reaper keeps the exited leader a zombie
// while such a member lives, so the group ID stays pinned (Kill signals it
// soundly) and Done stays open: the handle's process tree is still running.
func TestHandleKill_AfterLeaderExit_KillsDescendant(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "bg-launcher"}, zap.NewNop())
	require.NoError(t, err)
	hh := h.(*handle)

	pid := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.Eventually(t, func() bool { return !liveNonZombie(hh.cmd.Process.Pid) },
		10*time.Second, 20*time.Millisecond, "the launcher leader did not exit")
	select {
	case <-h.Done():
		t.Fatal("Done closed while a member of the leader's process group still runs")
	case <-time.After(300 * time.Millisecond):
	}
	require.True(t, liveNonZombie(pid), "control: the descendant outlives the leader")

	require.NoError(t, h.Kill())
	// Gone or a zombie: in a container whose PID 1 does not reap orphans
	// the killed descendant stays a zombie, which kill(pid, 0) still finds
	// (review round 3).
	require.Eventually(t, func() bool { return !liveNonZombie(pid) },
		5*time.Second, 20*time.Millisecond,
		"Kill after the leader exited must reach the descendant left in its process group")
	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the leader was not reaped once its group emptied")
	}
	require.NoError(t, h.Kill(), "Kill after the reap is a no-op")
}

// Review round 6: Stop on a launcher whose leader already exited but left a
// descendant in its group terminates the group (the zombie leader pins its
// ID) and returns once the group is empty and the leader is reaped.
func TestHandleStop_AfterLeaderExit_StopsDescendant(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "bg-stop", StopGrace: 2 * time.Second}, zap.NewNop())
	require.NoError(t, err)
	hh := h.(*handle)
	pid := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.Eventually(t, func() bool { return !liveNonZombie(hh.cmd.Process.Pid) },
		10*time.Second, 20*time.Millisecond, "the launcher leader did not exit")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, h.Stop(ctx))
	require.False(t, liveNonZombie(pid), "Stop must bring down the descendant left in the group")
}

// Where the group's members cannot be listed, the reaper falls back to
// killing what is left of the group before the reap, while the zombie
// leader still pins its ID (review round 6): the descendant does not
// outlive the handle unreachable.
func TestHandle_GroupScanUnavailable_KillsDescendantBeforeReap(t *testing.T) {
	fail := func(int, int) (bool, error) { return false, errors.New("injected: no group scan") }
	prev := groupScanHook.Swap(&fail)
	t.Cleanup(func() { groupScanHook.Store(prev) })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "bg-noscan"}, zap.NewNop())
	require.NoError(t, err)
	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher leader was not reaped")
	}
	pid := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.Eventually(t, func() bool { return !liveNonZombie(pid) },
		5*time.Second, 20*time.Millisecond,
		"the descendant must be killed before the leader is reaped")
}

// Sol review round 6: once the leader is reaped, no check can tell our
// group from an unrelated group that reused the number and whose own leader
// was reaped too while a descendant lives on (kill(pgid, 0) reports ESRCH
// for both). Kill after the reap must leave such a group alone.
func TestHandleKill_AfterGroupGone_DoesNotSignalReusedIDWithReapedLeader(t *testing.T) {
	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "short-reaped"}, zap.NewNop())
	require.NoError(t, err)
	<-h.Done()
	hh := h.(*handle)
	require.Positive(t, hh.pgid, "the group is recorded at spawn")

	// An unrelated group whose leader backgrounds a descendant and exits;
	// the leader is reaped, the descendant lives on in the group.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "other-descendant.pid")
	other := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	require.NoError(t, other.Wait())
	descendant := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(descendant, syscall.SIGKILL) })
	groupID := other.Process.Pid
	require.ErrorIs(t, syscall.Kill(groupID, 0), syscall.ESRCH, "control: the other group's leader is reaped")
	require.NoError(t, syscall.Kill(-groupID, 0), "control: the other group still has a member")

	hh.pgid = groupID
	require.NoError(t, h.Kill())
	time.Sleep(300 * time.Millisecond)
	require.True(t, liveNonZombie(descendant),
		"an unrelated group with a reused ID and a reaped leader was signalled")
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

// injectExitObservationFailure makes reap's non-reaping exit wait report
// failure, as kqueue under fd exhaustion or a denied waitid would.
func injectExitObservationFailure(t *testing.T) {
	t.Helper()
	fail := func(int) bool { return false }
	prev := exitObserverHook.Swap(&fail)
	t.Cleanup(func() { exitObserverHook.Store(prev) })
}

// Sol review round 5: when the exit could not be observed without reaping,
// reap fell back to a cmd.Wait outside reapMu, which reopened the round-4
// window. The fallback now reaps only under reapMu as well: a Kill that
// arrives after the real reap but before reaped is published waits for it
// and then leaves a reused group alone.
func TestHandleKill_ObservationFailure_AfterRealReapBeforePublish_DoesNotSignalReusedID(t *testing.T) {
	injectExitObservationFailure(t)
	inWindow := make(chan *handle, 1)
	releaseReaper := make(chan struct{})
	hook := func(h *handle) {
		if h.name == "reap-unobserved-window" {
			inWindow <- h
			<-releaseReaper
		}
	}
	prev := reapedLockedHook.Swap(&hook)
	t.Cleanup(func() { reapedLockedHook.Store(prev) })

	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "reap-unobserved-window"}, zap.NewNop())
	require.NoError(t, err)
	var hh *handle
	select {
	case hh = <-inWindow:
	case <-time.After(10 * time.Second):
		t.Fatal("the reaper did not reach the window after the real reap (the fallback reaped outside reapMu)")
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
		t.Fatal("an unrelated group with a reused ID was signalled between the fallback's real reap and publishing reaped")
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
	<-h.Done()
}

// The round-5 fallback must not hold up Kill while the leader lives (a
// blocking wait under reapMu would), must reach the group, and must still
// report the exit status.
func TestHandle_ObservationFailure_KillNotBlockedAndExitStatusKept(t *testing.T) {
	injectExitObservationFailure(t)

	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("sleep", "300"), Name: "unobserved-live"}, zap.NewNop())
	require.NoError(t, err)
	killed := make(chan error, 1)
	go func() { killed <- h.Kill() }()
	select {
	case err := <-killed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Kill was held up by the fallback reaper while the leader was alive")
	}
	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the fallback reaper did not reap the killed leader")
	}
	require.ErrorContains(t, h.Wait(), "signal: killed")

	h2, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 3"), Name: "unobserved-exit"}, zap.NewNop())
	require.NoError(t, err)
	<-h2.Done()
	require.EqualError(t, h2.Wait(), "exit status 3")

	h3, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "unobserved-ok"}, zap.NewNop())
	require.NoError(t, err)
	<-h3.Done()
	require.NoError(t, h3.Wait())
}
