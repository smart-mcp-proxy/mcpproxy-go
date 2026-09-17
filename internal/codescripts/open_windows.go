//go:build windows

package codescripts

import (
	"errors"
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
//
// Round 13 MUST-FIX (round-10 finding 4): the share mode widened from
// FILE_SHARE_READ alone to FILE_SHARE_READ|WRITE|DELETE — the same sharing
// os.Open itself requests (syscall.Open on windows: FILE_SHARE_READ|
// FILE_SHARE_WRITE) plus DELETE, so this read cannot itself block a
// concurrent atomic replace (rename-over) of the very file it is reading.
// Concrete failure this closes: an editor (or an atomic-write deploy of a
// new script version) holds the file open with delete sharing enabled —
// origin/main's os.Open could still read it; the round-11 CreateFile with
// FILE_SHARE_READ alone returned a sharing violation instead, a behavior
// change from the pre-Spec-105 administrator path that SC-005 does not
// call for, and this open in turn withheld FILE_SHARE_DELETE from ITS OWN
// handle, which would have blocked that same atomic replace for as long as
// this read holds the file open.
func openScriptFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		// Without FILE_FLAG_BACKUP_SEMANTICS (deliberately not requested: it
		// would let a process holding SeBackupPrivilege read past ACLs, which
		// os.Open never did) CreateFile refuses a DIRECTORY with
		// ERROR_ACCESS_DENIED before any attribute is visible. The
		// administrator's pre-105 answer for a directory candidate is
		// non-regular, not unreadable (SC-005), so classify that one case
		// from the attributes.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			if attrs, aerr := windows.GetFileAttributes(p); aerr == nil && attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
				return nil, errNonRegular
			}
		}
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
