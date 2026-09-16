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
// EXECUTED, not of a separate pre-open probe of the same path — a
// case-rename or replacement landing between the pre-open probe
// (storedSpellingsOf) and openScriptFile's own open would otherwise let the
// wrong spelling run, because NTFS folds the subsequent open onto whatever
// now occupies the name. GetFinalPathNameByHandle on the EXECUTED file's own
// handle is the call that reports the normalized path NTFS actually opened,
// unlike the requested path, which merely echoes what was asked for.
func openedEntryName(f *os.File) (string, error) {
	h := windows.Handle(f.Fd())
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
	return filepath.Base(windows.UTF16ToString(buf[:n])), nil
}
