//go:build linux

package server

import (
	"fmt"
	"syscall"
)

// getPeerCredentialsPlatform gets peer credentials using Linux SO_PEERCRED
func getPeerCredentialsPlatform(fd int) (*Ucred, error) {
	// Linux: Use SO_PEERCRED to get peer UID/GID/PID
	ucred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return nil, fmt.Errorf("SO_PEERCRED failed: %w", err)
	}

	return &Ucred{
		Pid: ucred.Pid,
		Uid: ucred.Uid,
		Gid: ucred.Gid,
	}, nil
}
