//go:build !windows

package server

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// isAddrInUseError determines whether an error represents an address-in-use condition.
func isAddrInUseError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}

	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.EADDRINUSE {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if isAddrInUseError(opErr.Err) {
			return true
		}
	}

	// Final fallback for platform-specific error strings.
	return strings.Contains(strings.ToLower(err.Error()), "address already in use")
}
