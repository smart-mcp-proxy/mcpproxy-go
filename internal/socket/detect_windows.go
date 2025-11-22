//go:build windows

package socket

import (
	"context"
	"time"
)

// isPipeAvailable checks if a Windows named pipe is available by attempting to connect.
func isPipeAvailable(endpoint string) bool {
	pipePath := extractPipePath(endpoint)
	if pipePath == "" {
		return false
	}

	// Try to connect with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	conn, err := dialNamedPipe(ctx, pipePath)
	if err != nil {
		return false
	}

	// Successfully connected - pipe is available
	conn.Close()
	return true
}
