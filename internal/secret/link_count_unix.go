//go:build unix

package secret

import (
	"fmt"
	"os"
	"syscall"
)

// handleLinkCount returns the number of hard links of the opened file, taken
// from the handle (fstat), not from a path.
func handleLinkCount(_ *os.File, info os.FileInfo) (uint64, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, fmt.Errorf("link count unavailable")
	}
	return uint64(st.Nlink), nil //nolint:unconvert // Nlink is uint16 on darwin, uint64 on linux
}
