//go:build windows

package secret

import "os"

func openSecretPath(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}

func openSecretInRoot(root *os.Root, rel string) (*os.File, error) {
	return root.OpenFile(rel, os.O_RDONLY, 0)
}

// openDirNoBlock opens a directory path for reading entries.
func openDirNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
