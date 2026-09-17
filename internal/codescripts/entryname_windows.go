//go:build windows

package codescripts

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// winFileNameNormalized and winVolumeNameDOS are GetFinalPathNameByHandle's
// dwFlags bits (VOLUME_NAME_DOS | FILE_NAME_NORMALIZED, both 0 — the default
// "\\?\C:\..." form); golang.org/x/sys/windows does not export Win32
// constants that are plain flag values rather than API surface, so they are
// named here from the documented Win32 API values.
const (
	winFileNameNormalized = 0x0
	winVolumeNameDOS      = 0x0
)

// entryName returns the name the filesystem actually stores for the directory
// entry at path, without following a reparse point and without listing the
// directory. NTFS is case-insensitive but case-PRESERVING: a probe for
// `backdoor.js` finds `backdoor.JS`, and FindFirstFile on the exact path is
// the single-entry lookup that reports the stored spelling (the same call the
// standard library's filepath.EvalSymlinks uses to normalise case).
func entryName(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(p, &data)
	if err != nil {
		return "", err
	}
	_ = windows.FindClose(h)
	return windows.UTF16ToString(data.FileName[:]), nil
}

// openedEntryName is entryName's post-open counterpart (round 9 MUST-FIX):
// it proves the stored spelling of the descriptor that will actually be
// EXECUTED, not of a separate pre-open probe of the same path. Superseded as
// the AUTHORITATIVE proof by openedFinalPath (round 11 MUST-FIX: a basename
// alone is satisfied by any identically named file reached through a
// retargeted reparse point — see storedspellings_probe_windows.go), but kept
// for entryNameFromFd's shared plumbing and any caller that only needs the
// base name.
func openedEntryName(f *os.File) (string, error) {
	full, err := finalPathOfHandle(windows.Handle(f.Fd()))
	if err != nil {
		return "", err
	}
	return filepath.Base(full), nil
}

// openedFinalPath is openedEntryName's FULL-PATH counterpart (round 11
// MUST-FIX, the reparse-point escape): the basename that openedEntryName
// reports is satisfied by any identically named file reachable through a
// reparse point planted between the pre-open probe and the open, so the
// authoritative proof must compare the descriptor's complete normalized
// path — parent directory included — against the scripts directory's own
// final path (dirFinalPath) plus the exact basename, not the basename
// alone.
func openedFinalPath(f *os.File) (string, error) {
	return finalPathOfHandle(windows.Handle(f.Fd()))
}

// dirFinalPath opens scriptsDir once — FILE_FLAG_BACKUP_SEMANTICS is
// required to obtain a handle on a directory at all — and returns its own
// normalized final path together with a func that releases the handle. This
// is the baseline openedFinalPath is compared against (round 11 MUST-FIX):
// confirming a candidate's PARENT is this exact directory, not merely that
// its basename matches, is what a retargeted reparse point on an ancestor
// cannot spoof.
func dirFinalPath(scriptsDir string) (path string, closeHandle func(), err error) {
	p, err := windows.UTF16PtrFromString(scriptsDir)
	if err != nil {
		return "", nil, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0)
	if err != nil {
		return "", nil, err
	}
	fp, err := finalPathOfHandle(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return "", nil, err
	}
	return fp, func() { _ = windows.CloseHandle(h) }, nil
}

// finalPathOfHandle is the shared GetFinalPathNameByHandle call: the
// normalized path NTFS actually resolved a handle to, unlike the path that
// was requested, which merely echoes what was asked for.
func finalPathOfHandle(h windows.Handle) (string, error) {
	flags := uint32(winFileNameNormalized | winVolumeNameDOS)

	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		// The path did not fit; n is the required length (including the
		// terminator) and the call did not error, so retry once at that size.
		buf = make([]uint16, n)
		n, err = windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err != nil {
			return "", err
		}
	}
	return windows.UTF16ToString(buf[:n]), nil
}
