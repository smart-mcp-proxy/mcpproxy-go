//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package launcher

import (
	"errors"
	"syscall"
)

// waitExited blocks until the process exits, without reaping it: a kqueue
// EVFILT_PROC/NOTE_EXIT event fires on exit and leaves the exited leader a
// zombie, which keeps its PID and process-group ID reserved, until reap
// calls cmd.Wait under reapMu (Spec 116 PR4, review round 4). The process
// is our unreaped child, so a registration refused with ESRCH means it
// already exited. It reports false when the exit could not be observed
// that way; reap then falls back to reapUnobserved.
func waitExited(pid int) bool {
	kq, err := syscall.Kqueue()
	if err != nil {
		return false
	}
	defer syscall.Close(kq)
	change := syscall.Kevent_t{}
	syscall.SetKevent(&change, pid, syscall.EVFILT_PROC, syscall.EV_ADD|syscall.EV_ONESHOT)
	change.Fflags = syscall.NOTE_EXIT
	events := make([]syscall.Kevent_t, 1)
	changes := []syscall.Kevent_t{change}
	for {
		n, err := syscall.Kevent(kq, changes, events, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			// The registration may or may not have been applied; EV_ADD
			// again is idempotent.
			continue
		case errors.Is(err, syscall.ESRCH):
			return true
		case err != nil:
			return false
		}
		if n > 0 {
			if events[0].Flags&syscall.EV_ERROR != 0 {
				return syscall.Errno(events[0].Data) == syscall.ESRCH
			}
			return true
		}
		changes = nil
	}
}
