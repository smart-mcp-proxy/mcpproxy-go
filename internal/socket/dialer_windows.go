//go:build windows

package socket

import (
	"context"
	"fmt"
	"net"
	"strings"

	winio "github.com/Microsoft/go-winio"
)

// CreateDialer creates a DialContext function for Windows named pipes.
func CreateDialer(endpoint string) (func(context.Context, string, string) (net.Conn, error), string, error) {
	if !isNamedPipe(endpoint) {
		return nil, endpoint, nil // Not a named pipe, use default
	}

	pipePath := extractPipePath(endpoint)
	if pipePath == "" {
		return nil, "", fmt.Errorf("invalid named pipe path in endpoint: %s", endpoint)
	}

	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialNamedPipe(ctx, pipePath)
	}

	// Return pipe dialer and dummy HTTP base URL
	return dialer, "http://localhost", nil
}

func isNamedPipe(endpoint string) bool {
	return strings.HasPrefix(endpoint, "npipe://")
}

func extractPipePath(endpoint string) string {
	if !strings.HasPrefix(endpoint, "npipe://") {
		return ""
	}

	// Named pipe path: npipe:////./pipe/name → //./pipe/name
	pipePath := strings.TrimPrefix(endpoint, "npipe://")
	pipePath = strings.TrimLeft(pipePath, "/")

	if strings.HasPrefix(pipePath, "./pipe/") {
		// Fix partial path: ./pipe/name → //./pipe/name
		pipePath = "//" + pipePath
	} else if !strings.HasPrefix(pipePath, "\\\\.\\") && !strings.HasPrefix(pipePath, "//./") {
		// Add Windows prefix if missing
		pipePath = "//./pipe/" + pipePath
	}

	return pipePath
}

func dialNamedPipe(ctx context.Context, pipePath string) (net.Conn, error) {
	// Use go-winio library for Windows named pipe support
	return winio.DialPipeContext(ctx, pipePath)
}
