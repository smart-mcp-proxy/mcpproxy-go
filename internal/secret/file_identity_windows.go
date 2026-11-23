//go:build windows

package secret

import (
	"errors"
	"os"
	"syscall"
)

// fileIdentity is a file's volume serial number and file index, read from an
// open handle. os.Stat on Windows fills the file ID lazily, by path, on the
// first os.SameFile, so a retained FileInfo would name whatever file sits at
// the path by then (the new config after a rename); this is captured eagerly.
type fileIdentity struct {
	vol uint32
	idx uint64
}

var errIdentityNotRegular = errors.New("not a regular file")

// identityShareMode lets the identity open coexist with any other handle:
// os.Open omits FILE_SHARE_DELETE, so it fails with a sharing violation while
// another process holds the file open with DELETE access.
const identityShareMode = syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE

// fileReadAttributes is FILE_READ_ATTRIBUTES, the only access
// GetFileInformationByHandle needs.
const fileReadAttributes = 0x0080

func identityFromHandle(h syscall.Handle) (fileIdentity, error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return fileIdentity{}, err
	}
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fileIdentity{}, errIdentityNotRegular
	}
	return fileIdentity{vol: d.VolumeSerialNumber, idx: uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)}, nil
}

// identityOfPathErr opens path (following links) with attribute-only access
// and every share flag, and reads its identity from the handle. A missing
// file reports an error matching fs.ErrNotExist.
func identityOfPathErr(path string) (fileIdentity, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fileIdentity{}, err
	}
	h, err := syscall.CreateFile(p, fileReadAttributes, identityShareMode, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fileIdentity{}, &os.PathError{Op: "CreateFile", Path: path, Err: err}
	}
	defer syscall.CloseHandle(h) //nolint:errcheck // read-only attribute handle
	return identityFromHandle(h)
}

func identityOfPath(path string) (fileIdentity, bool) {
	id, err := identityOfPathErr(path)
	return id, err == nil
}

// identityOfHandle returns the identity of an opened file.
func identityOfHandle(f *os.File, _ os.FileInfo) (fileIdentity, bool) {
	if f == nil {
		return fileIdentity{}, false
	}
	id, err := identityFromHandle(syscall.Handle(f.Fd()))
	return id, err == nil
}
