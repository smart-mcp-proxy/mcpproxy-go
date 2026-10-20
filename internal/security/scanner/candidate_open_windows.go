//go:build windows

package scanner

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openCandidateDescriptor is the Windows counterpart of the unix open, modelled
// on internal/codescripts/open_windows.go. FILE_FLAG_OPEN_REPARSE_POINT makes
// CreateFile open a symlink or junction ITSELF instead of resolving it (the
// O_NOFOLLOW equivalent), and GetFileInformationByHandle on that handle then
// refuses a reparse point or a directory, so the type decision is made on the
// handle and there is no check-then-open window. Windows has no filesystem
// FIFOs. The share mode includes FILE_SHARE_DELETE so this read cannot block an
// atomic replace of the bundle file by a publisher.
func openCandidateDescriptor(path string) (*os.File, int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, 0, err
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
		// would let a holder of SeBackupPrivilege read past ACLs) CreateFile
		// refuses a directory with ERROR_ACCESS_DENIED before any attribute is
		// visible; classify that one case from the attributes.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			if attrs, aerr := windows.GetFileAttributes(p); aerr == nil && attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
				return nil, 0, notRegularError("directory")
			}
		}
		return nil, 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		_ = windows.CloseHandle(h)
		return nil, 0, &os.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	switch {
	case fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		_ = windows.CloseHandle(h)
		return nil, 0, notRegularError("reparse point")
	case fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		_ = windows.CloseHandle(h)
		return nil, 0, notRegularError("directory")
	}
	size := int64(fi.FileSizeHigh)<<32 | int64(fi.FileSizeLow)
	return os.NewFile(uintptr(h), path), size, nil
}
