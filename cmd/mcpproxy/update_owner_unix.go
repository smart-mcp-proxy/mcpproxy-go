//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// describeOwner renders "owner: root (uid 0), mode drwxr-xr-x" for a path, so
// a refusal to write names who actually owns it (FR-022). Every lookup
// degrades gracefully: a diagnostic must never be the thing that fails.
func describeOwner(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "owner unknown: " + err.Error()
	}

	mode := fi.Mode().String()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Sprintf("mode %s", mode)
	}

	uid := strconv.FormatUint(uint64(st.Uid), 10)
	name := uid
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		name = u.Username
	}
	return fmt.Sprintf("owner: %s (uid %s), mode %s", name, uid, mode)
}
