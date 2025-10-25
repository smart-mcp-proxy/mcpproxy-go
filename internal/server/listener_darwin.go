//go:build darwin

package server

import (
	"fmt"
	"syscall"
	"unsafe"
)

// getPeerCredentialsPlatform gets peer credentials using macOS LOCAL_PEERCRED
func getPeerCredentialsPlatform(fd int) (*Ucred, error) {
	// macOS uses LOCAL_PEERCRED socket option
	// The structure is different from Linux

	// xucred structure for macOS (from sys/ucred.h)
	type xucred struct {
		Version uint32
		Uid     uint32
		NGroups int16
		Groups  [16]uint32
	}

	var cred xucred
	credLen := uint32(unsafe.Sizeof(cred))

	// LOCAL_PEERCRED = 0x001 (from sys/un.h)
	// SOL_LOCAL = 0 (from sys/socket.h)
	const SOL_LOCAL = 0
	const LOCAL_PEERCRED = 0x001

	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(SOL_LOCAL),
		uintptr(LOCAL_PEERCRED),
		uintptr(unsafe.Pointer(&cred)),
		uintptr(unsafe.Pointer(&credLen)),
		0,
	)

	if errno != 0 {
		return nil, fmt.Errorf("LOCAL_PEERCRED failed: %v", errno)
	}

	// macOS doesn't provide PID through LOCAL_PEERCRED
	return &Ucred{
		Pid: -1, // Not available on macOS
		Uid: cred.Uid,
		Gid: cred.Groups[0], // Primary group
	}, nil
}
