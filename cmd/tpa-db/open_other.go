//go:build !unix

package main

import "os"

// openRegular opens path for reading. There are no FIFOs to block on here;
// readBounded's lstat and descriptor identity checks refuse anything that is
// not a regular file.
func openRegular(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // operator-supplied path; type-checked by readBounded
}
