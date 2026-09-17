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

// Round 13 MUST-FIX (round-10 findings 2 and 3 — unify Windows onto the
// index + retained-directory-handle design storednames_windows.go now
// builds): the path-based single-entry lookups this file used to hold
// (entryName/FindFirstFile, dirFinalPath, openedFinalPath, a full-path
// baseline comparison) are gone. storedExactly now answers from the same
// per-directory exact-spelling INDEX every unix platform uses (an absent
// name and a present case-variant are both plain index misses — closing
// finding 3's timing oracle for Windows too), and both the candidate probe
// and the actual open are performed RELATIVE TO ONE RETAINED DIRECTORY
// HANDLE via NtCreateFile with RootDirectory set (storednames_windows.go) —
// a rename of the directory, or a reparse point planted on an ancestor,
// cannot redirect a relative open the way it could a fresh path lookup
// (finding 2). Because the open is already structurally bound to the
// retained handle, the post-open proof needs only the opened descriptor's
// own BASENAME (winOpenedBaseName, storednames_windows.go) — the parent is
// no longer in question — so this file keeps just finalPathOfHandle, the
// shared GetFinalPathNameByHandle call that proof uses.
func openedBaseName(f *os.File) (string, error) {
	full, err := finalPathOfHandle(windows.Handle(f.Fd()))
	if err != nil {
		return "", err
	}
	return filepath.Base(full), nil
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
