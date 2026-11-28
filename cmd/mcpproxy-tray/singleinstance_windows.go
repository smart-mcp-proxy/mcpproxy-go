//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// singleInstanceMutexName is Local\<id>-<user SID>: per logon session, and
// per user within it, so a tray started with "Run as different user" in the
// same session is not turned away by this user's tray.
func singleInstanceMutexName(id string) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return `Local\` + id + "-" + user.User.Sid.String(), nil
}

// acquireSingleInstance claims the named mutex singleInstanceMutexName(id)
// and reports whether this process is the only one holding it. The handle is
// never closed: Windows releases it when the process exits, however it exits.
func acquireSingleInstance(id string) (bool, error) {
	mutexName, err := singleInstanceMutexName(id)
	if err != nil {
		return false, err
	}
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		// The handle is valid. Close it now: held while this process logs and
		// exits, it would keep the mutex alive after the running tray quits
		// and turn away the next launch too.
		_ = windows.CloseHandle(handle)
		return false, nil
	// ACCESS_DENIED: the mutex exists but belongs to an elevated instance.
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return false, nil
	default:
		return false, err
	}
}
