//go:build unix

package launcher

import (
	"os/exec"
	"syscall"

	"go.uber.org/zap"
)

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
