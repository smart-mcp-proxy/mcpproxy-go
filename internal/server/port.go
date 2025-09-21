package server

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
)

// PortInUseError indicates that the requested listen address is already occupied.
type PortInUseError struct {
	Address string
	Err     error
}

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("port %s is already in use", e.Address)
}

func (e *PortInUseError) Unwrap() error {
	return e.Err
}

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

// findAvailableListenAddress returns an available listen address derived from baseAddr.
// When the base port is 0, the operating system will pick a free port.
func findAvailableListenAddress(baseAddr string, attempts int) (string, error) {
	host, port, err := splitListenAddress(baseAddr)
	if err != nil {
		return "", err
	}

	// If the caller explicitly requests an ephemeral port, honour it directly.
	if port == 0 {
		return probeAvailableAddress(host, port)
	}

	// Ensure attempts is sane.
	if attempts <= 0 {
		attempts = 10
	}

	for i := 1; i <= attempts; i++ {
		candidatePort := port + i
		availableAddr, probeErr := probeAvailableAddress(host, candidatePort)
		if probeErr == nil {
			return availableAddr, nil
		}
		if !isAddrInUseError(probeErr) {
			// Unexpected error (e.g., permission denied). Try the next port regardless.
			continue
		}
	}

	return "", fmt.Errorf("unable to find available port near %s", baseAddr)
}

// probeAvailableAddress attempts to listen on the provided host/port and returns the
// concrete address reported by the OS. The listener is closed before returning.
func probeAvailableAddress(host string, port int) (string, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

// splitListenAddress parses a listen string into host and port components.
func splitListenAddress(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, fmt.Errorf("listen address cannot be empty")
	}

	if !strings.Contains(addr, ":") {
		return "", 0, fmt.Errorf("listen address %q must include a port", addr)
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address %q: %w", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	if port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %d is out of range", port)
	}

	return host, port, nil
}
