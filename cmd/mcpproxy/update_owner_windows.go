//go:build windows

package main

import (
	"fmt"
	"os"
)

// describeOwner reports what Windows makes cheaply available. Resolving the
// security descriptor's owner SID needs golang.org/x/sys/windows plumbing that
// would only ever feed an error message, so the mode is enough context here
// (FR-022 requires naming the path; the owner is best-effort).
func describeOwner(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "owner unknown: " + err.Error()
	}
	return fmt.Sprintf("mode %s", fi.Mode().String())
}
