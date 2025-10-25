//go:build (darwin || linux) && !windows

package api

import (
	"context"
	"fmt"
	"net"
)

// dialNamedPipe is a stub for Unix platforms (not supported)
//
//nolint:unused // Platform-specific stub, used only on Windows
func dialNamedPipe(ctx context.Context, pipePath string) (net.Conn, error) {
	return nil, fmt.Errorf("named pipes are only supported on Windows")
}
