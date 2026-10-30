//go:build windows

package config

import "os"

// sameFileGroup: Windows has no POSIX file groups, and Go maps only the
// owner-write bit onto the read-only attribute, so a mode change cannot open
// the file to anyone.
func sameFileGroup(*os.File, os.FileInfo) bool { return true }
