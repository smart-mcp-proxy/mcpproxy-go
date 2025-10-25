//go:build windows

package api

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// dialNamedPipe connects to a Windows named pipe
func dialNamedPipe(ctx context.Context, pipePath string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipePath)
}
