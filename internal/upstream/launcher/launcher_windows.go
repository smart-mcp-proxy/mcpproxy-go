//go:build windows

package launcher

import (
	"os/exec"

	"go.uber.org/zap"
)

// applyProcAttrs is a no-op on Windows. Windows uses Job Objects rather
// than process groups; integrating with the existing Windows process
// management is left to a follow-up (matching the TODO already in
// internal/upstream/core/process_windows.go).
func applyProcAttrs(_ *exec.Cmd) {}

// terminateProcess on Windows just signals the child directly. Without
// Job Objects we cannot reach grandchildren; the existing stdio path has
// the same limitation today.
func terminateProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func killProcess(cmd *exec.Cmd, _ *zap.Logger) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
