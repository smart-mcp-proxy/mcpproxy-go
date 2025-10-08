//go:build windows

package server

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// isAddrInUseError determines whether an error represents an address-in-use condition.
// Windows-specific implementation that handles WSAEADDRINUSE (10048).
func isAddrInUseError(err error) bool {
	if err == nil {
		return false
	}

	// Check for WSAEADDRINUSE (10048) on Windows
	const WSAEADDRINUSE = syscall.Errno(10048)

	if errors.Is(err, WSAEADDRINUSE) {
		return true
	}

	var errno syscall.Errno
	if errors.As(err, &errno) && errno == WSAEADDRINUSE {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if isAddrInUseError(opErr.Err) {
			return true
		}
	}

	// Final fallback for platform-specific error strings.
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "address already in use") ||
		strings.Contains(errStr, "only one usage of each socket address")
}
