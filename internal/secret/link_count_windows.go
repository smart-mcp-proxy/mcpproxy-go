//go:build windows

package secret

import (
	"os"
	"syscall"
)

// handleLinkCount returns the number of hard links of the opened file, read
// from the handle with GetFileInformationByHandle.
func handleLinkCount(f *os.File, _ os.FileInfo) (uint64, error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, err
	}
	return uint64(d.NumberOfLinks), nil
}
