//go:build windows

package codescripts

import (
	"fmt"
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

// getFinalPathNameByHandle is windows.GetFinalPathNameByHandle as a seam:
// entryname_windows_test.go replaces it to drive the retry logic in
// finalPathOfHandle at exact buffer-size boundaries, which no real handle
// can be made to hit deterministically (it would need a path whose
// normalized UTF-16 length is exactly 1024 units).
var getFinalPathNameByHandle = windows.GetFinalPathNameByHandle

// finalPathNameMaxAttempts bounds finalPathOfHandle's resize-and-retry loop
// (round 15 MUST-FIX): the path GetFinalPathNameByHandle resolves a handle
// to can keep growing between calls — e.g. another process renames the
// file to a longer path while this delete-shareable handle stays open — so
// a single retry sized to one stale report can still be too small. Bounded
// rather than unbounded so a pathologically fast renamer cannot spin this
// forever; four attempts is generous headroom over the one legitimate
// undersized-then-exact-fit retry this ever needs in practice.
const finalPathNameMaxAttempts = 4

// finalPathOfHandle is the shared GetFinalPathNameByHandle call: the
// normalized path NTFS actually resolved a handle to, unlike the path that
// was requested, which merely echoes what was asked for.
func finalPathOfHandle(h windows.Handle) (string, error) {
	flags := uint32(winFileNameNormalized | winVolumeNameDOS)

	buf := make([]uint16, 1024)
	for attempt := 0; attempt < finalPathNameMaxAttempts; attempt++ {
		n, err := getFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			// The call succeeded within this buffer — n is the resolved
			// length EXCLUDING the terminator here, unlike the
			// undersized-buffer case below. Only now is buf[:n] safe to
			// slice.
			return windows.UTF16ToString(buf[:n]), nil
		}
		// The path did not fit; when the buffer was too small, n is the
		// required length INCLUDING the terminator, and the call does not
		// error, so n == len(buf) also means truncation (an exact-length
		// path leaves no room for the terminator), not only n > len(buf).
		// Resize to exactly that reported size and retry — the resize
		// itself is not the last word, because the path can have grown
		// again by the time the retry lands (see finalPathNameMaxAttempts).
		buf = make([]uint16, n)
	}
	// Exhausted the bound without a call ever reporting a length that fit
	// the buffer it was given: the path is growing faster than we can size
	// for it (or something is persistently wrong). Return a plain error
	// rather than slicing a stale/undersized buffer — the caller
	// (winOpenedBaseName's verifyUnchanged) already treats any non-nil
	// error here the same as a spelling mismatch, folding it into
	// errSpellingUnproven, a non-disclosing refusal (SC-005).
	return "", fmt.Errorf("codescripts: GetFinalPathNameByHandle did not settle within %d attempts", finalPathNameMaxAttempts)
}
