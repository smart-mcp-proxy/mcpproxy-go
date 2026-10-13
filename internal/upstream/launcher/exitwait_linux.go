//go:build linux

package launcher

import (
	"errors"

	"golang.org/x/sys/unix"
)

// waitExited blocks until the process exits, without reaping it (waitid
// with WNOWAIT): the exited leader stays a zombie, which keeps its PID and
// process-group ID reserved, until reap calls cmd.Wait under reapMu (Spec
// 116 PR4, review round 4). It reports false when the exit could not be
// observed that way; reap then falls back to reapUnobserved.
func waitExited(pid int) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == nil {
			return true
		}
		if !errors.Is(err, unix.EINTR) {
			return false
		}
	}
}
