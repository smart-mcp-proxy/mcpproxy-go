//go:build unix

package launcher

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// createJob is a no-op on Unix: applyProcAttrs' process-group setup already
// gives terminateProcess/killProcess a way to reach grandchildren, so there
// is nothing extra to track here. See launcher_windows.go for the platform
// that actually needs this.
func createJob(_ *exec.Cmd) (io.Closer, error) {
	return nil, nil
}

// applyProcAttrs places the child in its own process group so we can signal
// the entire group (including grandchildren spawned via `sh -c …` or
// `docker run`) when stopping.
func applyProcAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

// terminateProcess sends SIGTERM to the child's process group.
func terminateProcess(cmd *exec.Cmd, log *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid <= 0 {
		// Fall back to signaling just the leader if pgid lookup
		// failed (e.g. the child already exited).
		log.Debug("getpgid failed, signaling pid directly", zap.Error(err))
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

// killProcess sends SIGKILL to the child's process group.
func killProcess(cmd *exec.Cmd, log *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid <= 0 {
		log.Debug("getpgid failed, killing pid directly", zap.Error(err))
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// terminate and kill are the two escalation steps stopLocked drives. On
// Unix they signal the recorded process group, under reapMu and only while
// the leader is not reaped (review round 6): the reaper keeps an exited
// leader a zombie while another member of its group runs, so Stop reaches
// a descendant the launcher left behind, and a group ID released by the
// reap is never signalled.
func (h *handle) terminate() error { return h.signalUnreaped(syscall.SIGTERM) }
func (h *handle) kill() error      { return h.signalUnreaped(syscall.SIGKILL) }

func (h *handle) signalUnreaped(sig syscall.Signal) error {
	h.reapMu.Lock()
	defer h.reapMu.Unlock()
	if h.reaped {
		return nil
	}
	if sig == syscall.SIGKILL {
		h.killed.Store(true)
	}
	return h.signalLocked(sig)
}

// signalLocked signals the leader's process group. The caller holds reapMu
// and has checked that the leader is not reaped, so the leader, alive or a
// zombie, still holds the group ID.
func (h *handle) signalLocked(sig syscall.Signal) error {
	if h.pgid <= 0 {
		if sig == syscall.SIGTERM {
			return terminateProcess(h.cmd, h.log)
		}
		return killProcess(h.cmd, h.log)
	}
	if err := syscall.Kill(-h.pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// startedGroupID is the process group the child leads: applyProcAttrs
// starts it with Setpgid and Pgid 0, so the group ID is its PID.
func startedGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil || cmd.SysProcAttr == nil {
		return 0
	}
	if cmd.SysProcAttr.Setpgid && cmd.SysProcAttr.Pgid == 0 {
		return cmd.Process.Pid
	}
	return 0
}

// retireKill is Handle.Kill on Unix: SIGKILL to the recorded process group
// while the leader is not yet reaped.
//
// While reaped is false the leader, alive or a zombie, holds the group ID,
// so the number cannot have been reused and the signal reaches only our
// group. reaped is set under reapMu, and the reap itself (cmd.Wait after a
// non-reaping wait for the exit, or the non-blocking wait4 of
// reapUnobserved) runs under the same lock (review rounds 3 to 5), so the
// leader cannot be reaped between this check and the signal.
//
// After the reap the group is never signalled (review round 6). No check can
// tell our group from an unrelated group that reused the number once its
// own leader was reaped too: kill(pgid, 0) reports ESRCH for both, and any
// check followed by a signal races with reuse anyway. Instead the reaper
// does not reap an exited leader while another member of its group runs
// (awaitGroupEmpty), so a descendant the launcher backgrounded (review
// round 2) is reached by this kill through the pinned group ID.
func (h *handle) retireKill() error {
	return h.signalUnreaped(syscall.SIGKILL)
}

// groupScanMaxPoll caps the poll interval of awaitGroupEmpty.
const groupScanMaxPoll = 250 * time.Millisecond

// awaitGroupEmpty runs in the reaper after the leader's exit was observed
// without reaping it (review round 6). It returns once no member of the
// leader's process group other than the zombie leader is running, so the
// zombie keeps the group ID pinned for as long as a descendant the launcher
// left behind runs: Kill and Stop can signal the group soundly in that
// time, and Done stays open while the handle's process tree runs.
//
// When the members cannot be listed (groupHasOtherMember fails, or the
// platform has no listing), it returns at once, and killGroupBeforeReap
// takes the remaining members down before the reap instead.
func (h *handle) awaitGroupEmpty() {
	if h.pgid <= 0 {
		return
	}
	leader := h.cmd.Process.Pid
	delay := 10 * time.Millisecond
	for {
		others, err := scanGroup(h.pgid, leader)
		if err != nil {
			h.log.Debug("cannot list the exited leader's process group; killing it before the reap",
				zap.Error(err))
			return
		}
		if !others {
			return
		}
		time.Sleep(delay)
		if delay *= 2; delay > groupScanMaxPoll {
			delay = groupScanMaxPoll
		}
	}
}

// repeatKillInterval is how often repeatKillUntilReaped re-sends SIGKILL.
const repeatKillInterval = 50 * time.Millisecond

// repeatKillUntilReaped runs for the whole of reap and returns a stop
// function. Once a SIGKILL was requested (Kill, Stop's escalation or a
// retirement) it repeats the signal to the group until the leader is reaped.
// A member forked while the first signal was delivered is not in the group
// yet when the kernel walks it, so it survives that kill; without a repeat
// the exited leader stays unreaped, and Done stays open, for as long as that
// member runs (forever for a daemon). It must run beside the reaper's pump
// drain too: the missed member holds the output pipes, so the pumps (and
// with them awaitGroupEmpty) cannot finish until it dies. Every signal is
// sent under reapMu with the leader not reaped, so the leader, alive or a
// zombie, still pins the group ID.
func (h *handle) repeatKillUntilReaped() (stop func()) {
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(repeatKillInterval)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
			}
			if !h.killed.Load() {
				continue
			}
			h.reapMu.Lock()
			if !h.reaped {
				_ = h.signalLocked(syscall.SIGKILL)
			}
			h.reapMu.Unlock()
		}
	}()
	return func() {
		close(quit)
		<-finished
	}
}

// killGroupBeforeReap runs in the reaper under reapMu, after awaitGroupEmpty
// and before cmd.Wait reaps the leader. The zombie leader still holds the
// group ID, so the SIGKILL reaches only what is left of our own group:
// nothing when the listing showed the group empty, and on the fallback
// (members could not be listed) any descendant still running, which would
// otherwise outlive the handle where no later Kill could safely reach it.
//
// reapUnobserved (the degraded path) cannot call it: it learns of the exit
// only by reaping, after which the group ID is no longer pinned. On that
// path a descendant left in the group is not killed (documented
// assumption: an orphan is the safe failure, signalling a reused group is
// not).
func (h *handle) killGroupBeforeReap() {
	if h.pgid <= 0 {
		return
	}
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		h.log.Debug("killing the exited leader's process group failed", zap.Error(err))
	}
}

// reapUnobservedMaxPoll caps the poll interval of reapUnobserved.
const reapUnobservedMaxPoll = 50 * time.Millisecond

// reapUnobserved reaps the leader when waitExited could not observe its
// exit (Spec 116 PR4, review round 5): kqueue refused under fd exhaustion,
// or waitid denied. A blocking cmd.Wait outside reapMu would release the
// leader's PID, and with it the group ID, while reaped is still false, and
// a concurrent retireKill would then signal the number unchecked. Holding
// reapMu across a blocking wait instead would stall Kill until the child it
// is meant to kill exits.
//
// So the reap is a non-blocking wait4 polled under reapMu: each attempt
// either finds the leader still running or a zombie (nothing released) or
// reaps it and publishes reaped before the lock is dropped. cmd.Wait then
// runs only for its cleanup (pipes, copying goroutines); the leader is
// already reaped, so its own wait reports ECHILD and is ignored, and the
// exit status comes from the wait4 status instead (a plain error, not an
// *exec.ExitError, on this degraded path — documented assumption).
//
// ECHILD from wait4 means the leader is no longer our unreaped child, so it
// is treated as reaped. Any other error keeps polling: the leader has not
// been released, so reaped stays false, which is the safe state for Kill.
func (h *handle) reapUnobserved() (int, error) {
	pid := h.cmd.Process.Pid
	var ws syscall.WaitStatus
	collected := false
	delay := time.Millisecond
	for {
		h.reapMu.Lock()
		wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			h.reapMu.Unlock()
			continue
		}
		if (err == nil && wpid == pid) || errors.Is(err, syscall.ECHILD) {
			collected = err == nil
			if hook := reapedLockedHook.Load(); hook != nil {
				(*hook)(h)
			}
			h.reaped = true
			h.reapMu.Unlock()
			break
		}
		h.reapMu.Unlock()
		time.Sleep(delay)
		if delay *= 2; delay > reapUnobservedMaxPoll {
			delay = reapUnobservedMaxPoll
		}
	}

	waitErr := h.cmd.Wait()
	// No Process.Release here: it rewrites Process.Pid, which Kill and
	// Spawn read concurrently. A pidfd left open by the failed wait is
	// closed by the os.Process cleanup when the handle is collected.
	if !collected {
		return -1, waitErr
	}
	switch {
	case ws.Exited():
		if code := ws.ExitStatus(); code != 0 {
			return code, fmt.Errorf("exit status %d", code)
		}
		return 0, nil
	case ws.Signaled():
		return -1, fmt.Errorf("signal: %v", ws.Signal())
	default:
		return -1, waitErr
	}
}

// groupScanHook replaces groupHasOtherMember in awaitGroupEmpty. A test
// seam that injects a listing failure; nil in production.
var groupScanHook atomic.Pointer[func(pgid, leader int) (bool, error)]

func scanGroup(pgid, leader int) (bool, error) {
	if hook := groupScanHook.Load(); hook != nil {
		return (*hook)(pgid, leader)
	}
	return groupHasOtherMember(pgid, leader)
}
