//go:build !windows

package config

import (
	"os"
	"syscall"
)

// sameFileGroup reports whether f is owned by the same group as other.
func sameFileGroup(f *os.File, other os.FileInfo) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	a, okA := info.Sys().(*syscall.Stat_t)
	b, okB := other.Sys().(*syscall.Stat_t)
	return okA && okB && a.Gid == b.Gid
}
