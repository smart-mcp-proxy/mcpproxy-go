//go:build darwin

package launcher

import "golang.org/x/sys/unix"

// sZomb is the zombie process state (p_stat SZOMB in <sys/proc.h>).
const sZomb = 5

// groupHasOtherMember reports whether a process other than leader that is
// not a zombie belongs to process group pgid (review round 6), listed with
// the kern.proc.pgrp sysctl.
func groupHasOtherMember(pgid, leader int) (bool, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return false, err
	}
	for i := range procs {
		p := &procs[i]
		if int(p.Proc.P_pid) == leader || p.Proc.P_stat == sZomb {
			continue
		}
		if int(p.Eproc.Pgid) == pgid {
			return true, nil
		}
	}
	return false, nil
}
