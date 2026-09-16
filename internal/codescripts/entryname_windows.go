//go:build windows

package codescripts

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

// entryName returns the name the filesystem actually stores for the directory
// entry at path (probed is its Lstat result, unused here), without following
// a reparse point and without listing the directory. NTFS is case-insensitive but case-PRESERVING: a probe for
// `backdoor.js` finds `backdoor.JS`, and FindFirstFile on the exact path is
// the single-entry lookup that reports the stored spelling (the same call the
// standard library's filepath.EvalSymlinks uses to normalise case).
func entryName(path string, _ fs.FileInfo) (string, error) {
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
