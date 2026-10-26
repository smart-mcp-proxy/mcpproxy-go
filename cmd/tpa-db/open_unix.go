//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openRegular opens path for reading without following a final symlink and
// without blocking on a FIFO (O_NONBLOCK makes open(2) of a writer-less FIFO
// return instead of waiting), then refuses anything the DESCRIPTOR says is not
// a regular file before clearing O_NONBLOCK for the read.
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) {
			return nil, fmt.Errorf("%s is not a regular file (symlink)", path)
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = syscall.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		_ = syscall.Close(fd)
		return nil, &os.PathError{Op: "setnonblock", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
