//go:build windows

package secret

import (
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

func identityFromHandle(h syscall.Handle) (fileIdentity, bool) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return fileIdentity{}, false
	}
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fileIdentity{}, false
	}
	return fileIdentity{vol: d.VolumeSerialNumber, idx: uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)}, true
}

// identityOfPath opens path (following links) and reads its identity from the
// handle.
func identityOfPath(path string) (fileIdentity, bool) {
	f, err := os.Open(path)
	if err != nil {
		return fileIdentity{}, false
	}
	defer f.Close()
	return identityFromHandle(syscall.Handle(f.Fd()))
}

// identityOfHandle returns the identity of an opened file.
func identityOfHandle(f *os.File, _ os.FileInfo) (fileIdentity, bool) {
	if f == nil {
		return fileIdentity{}, false
	}
	return identityFromHandle(syscall.Handle(f.Fd()))
}
