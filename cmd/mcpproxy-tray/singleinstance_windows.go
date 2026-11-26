//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// acquireSingleInstance claims the named mutex Local\<id> for this logon
// session and reports whether this process is the only one holding it. The
// handle is never closed: Windows releases it when the process exits, however
// it exits.
func acquireSingleInstance(id string) (bool, error) {
	name, err := windows.UTF16PtrFromString(`Local\` + id)
	if err != nil {
		return false, err
	}
	_, err = windows.CreateMutex(nil, false, name)
	switch {
	case err == nil:
		return true, nil
	// ACCESS_DENIED: the mutex exists but belongs to an elevated instance.
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return false, nil
	default:
		return false, err
	}
}
