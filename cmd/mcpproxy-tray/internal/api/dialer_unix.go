//go:build (darwin || linux) && !windows

package api

import (
	"context"
	"fmt"
	"net"
)

// dialNamedPipe is a stub for Unix platforms (not supported)
func dialNamedPipe(ctx context.Context, pipePath string) (net.Conn, error) {
	return nil, fmt.Errorf("named pipes are only supported on Windows")
}
