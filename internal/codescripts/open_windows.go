//go:build windows

package codescripts

import (
	"os"

	"golang.org/x/sys/windows"
)

// openScriptFile opens a stored script for reading without ever following a
// reparse point at the final path component (round 11 MUST-FIX). Earlier
// rounds Lstat'ed the path to rule out a symlink/junction and then called
// os.Open, which DOES follow a reparse point: a symlink or junction planted
// between the Lstat and the Open — or a symlinked ANCESTOR directory
// retargeted the same way — is followed straight through to whatever it now
// points at, and the caller's own descriptor-spelling proof used to compare
// only a basename (round 9), which an identically named file reached
// through the reparse point satisfies just as well.
//
// FILE_FLAG_OPEN_REPARSE_POINT makes CreateFile open the reparse point
// ITSELF rather than transparently resolving it — the Windows equivalent of
// O_NOFOLLOW — so there is no check-then-open window: whatever the entry
// is, this is the handle it opens, atomically. GetFileInformationByHandle on
// that handle then refuses a reparse point or a directory outright, exactly
// as O_NOFOLLOW plus the regular-file Fstat check does on Unix.
func openScriptFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		return nil, err
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if fi.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(h)
		return nil, errNonRegular
	}
	return os.NewFile(uintptr(h), path), nil
}
