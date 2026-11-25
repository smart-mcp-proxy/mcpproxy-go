//go:build unix

package secret

import (
	"os"
	"syscall"
)

const secretOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK

func openSecretPath(path string) (*os.File, error) {
	return os.OpenFile(path, secretOpenFlags, 0)
}

func openSecretInRoot(root *os.Root, rel string) (*os.File, error) {
	return root.OpenFile(rel, secretOpenFlags, 0)
}

// openDirNoBlock opens a directory path for reading entries. O_NONBLOCK keeps
// a path that was swapped for a FIFO from blocking the open; ReadDir then fails
// on it.
func openDirNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, secretOpenFlags, 0)
}
