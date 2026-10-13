//go:build linux

package launcher

import (
	"os"
	"strconv"
	"strings"
)

// groupHasOtherMember reports whether a process other than leader that is
// not a zombie belongs to process group pgid (review round 6), listed from
// /proc/<pid>/stat. A process that vanishes during the scan is skipped.
func groupHasOtherMember(pgid, leader int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == leader {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp ...: comm may contain spaces and
		// parentheses, so the fields start after the last ')'.
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 3 || f[0] == "Z" || f[0] == "X" {
			continue
		}
		if g, err := strconv.Atoi(f[2]); err == nil && g == pgid {
			return true, nil
		}
	}
	return false, nil
}
