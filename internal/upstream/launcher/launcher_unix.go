//go:build unix

package launcher

import (
	"errors"
	"io"
	"os/exec"
	"syscall"

	"go.uber.org/zap"
)

// createJob is a no-op on Unix: applyProcAttrs' process-group setup already
// gives terminateProcess/killProcess a way to reach grandchildren, so there
// is nothing extra to track here. See launcher_windows.go for the platform
// that actually needs this.
func createJob(_ *exec.Cmd) io.Closer {
	return nil
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
// Unix they are the process-group signals above; the Job Object argument
// the Windows build needs does not exist here.
func (h *handle) terminate() error { return terminateProcess(h.cmd, h.log) }
func (h *handle) kill() error      { return killProcess(h.cmd, h.log) }

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

// retireKill is Handle.Kill on Unix: SIGKILL to the recorded process group,
// also after the leader exited (Spec 116 PR4, review round 2). A launcher
// that backgrounds a descendant with redirected streams and exits closes
// Done while the descendant runs on in the group.
//
// After the leader was reaped the group ID is ours only while a member of
// the group lives, and while one does the kernel does not hand the number
// out as a PID. A live process holding the number as its PID therefore means
// our group is gone and the number was reused: the group is left alone. (The
// leader may be reaped a moment before Done closes; that window is a few
// instructions long and the number cannot be reused in it in practice.)
func (h *handle) retireKill() error {
	if h.pgid <= 0 {
		select {
		case <-h.done:
			return nil
		default:
		}
		return h.kill()
	}
	select {
	case <-h.done:
		if err := syscall.Kill(h.pgid, 0); !errors.Is(err, syscall.ESRCH) {
			return nil
		}
	default:
	}
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
