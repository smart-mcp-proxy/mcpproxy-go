//go:build !windows

package socket

import (
	"context"
	"fmt"
	"net"
)

// CreateDialer creates a DialContext function for Unix domain sockets.
func CreateDialer(endpoint string) (func(context.Context, string, string) (net.Conn, error), string, error) {
	if !isUnixSocket(endpoint) {
		return nil, endpoint, nil // Not a Unix socket, use default
	}

	socketPath := extractUnixSocketPath(endpoint)
	if socketPath == "" {
		return nil, "", fmt.Errorf("invalid unix socket path in endpoint: %s", endpoint)
	}

	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}

	// Return socket dialer and dummy HTTP base URL
	return dialer, "http://localhost", nil
}

func isUnixSocket(endpoint string) bool {
	return len(endpoint) >= 7 && endpoint[:7] == "unix://"
}

func extractUnixSocketPath(endpoint string) string {
	if len(endpoint) < 7 {
		return ""
	}
	path := endpoint[7:] // Remove "unix://"
	if path == "" {
		return ""
	}
	return path
}
