//go:build unix

package scanner

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openCandidateDescriptor opens path for reading without following a symlink
// at the final component and without ever blocking on the open, then decides
// everything about the file from the DESCRIPTOR (fstat), never the path.
//
//   - O_NOFOLLOW makes the kernel refuse a symlink atomically (ELOOP; some BSDs
//     return EMLINK), so there is no check-then-open window.
//   - O_NONBLOCK keeps a FIFO's open from waiting for a writer; the fstat that
//     follows then refuses it. The flag is a no-op for regular files.
//   - O_CLOEXEC keeps the descriptor out of any child process.
//
// The raw syscall is used rather than os.OpenFile so the nonblocking flag can
// be cleared before the descriptor is wrapped: os.NewFile registers a
// nonblocking descriptor with the runtime poller, and a regular file does not
// belong there.
func openCandidateDescriptor(path string) (*os.File, int64, error) {
	var fd int
	for {
		var err error
		fd, err = syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) {
			return nil, 0, notRegularError("symbolic link")
		}
		return nil, 0, &os.PathError{Op: "open", Path: path, Err: err}
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = syscall.Close(fd)
		return nil, 0, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = syscall.Close(fd)
		return nil, 0, notRegularError(fmt.Sprintf("file type %s", unixTypeName(uint32(st.Mode)&uint32(syscall.S_IFMT))))
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		_ = syscall.Close(fd)
		return nil, 0, &os.PathError{Op: "fcntl", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), int64(st.Size), nil
}

// unixTypeName names an S_IFMT value for the rejection detail.
func unixTypeName(t uint32) string {
	switch t {
	case syscall.S_IFIFO:
		return "fifo"
	case syscall.S_IFDIR:
		return "directory"
	case syscall.S_IFCHR:
		return "character device"
	case syscall.S_IFBLK:
		return "block device"
	case syscall.S_IFSOCK:
		return "socket"
	case syscall.S_IFLNK:
		return "symbolic link"
	default:
		return fmt.Sprintf("%#o", t)
	}
}
