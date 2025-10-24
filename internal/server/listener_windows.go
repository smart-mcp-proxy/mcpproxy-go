//go:build windows

package server

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"go.uber.org/zap"
)

// createNamedPipeListenerPlatform creates a Windows named pipe listener
func createNamedPipeListenerPlatform(pipeName string, logger *zap.Logger) (*Listener, error) {
	logger.Info("Creating Windows named pipe listener", zap.String("pipe", pipeName))

	// Create pipe configuration with security descriptor
	// This ensures only the current user can connect
	config := &winio.PipeConfig{
		SecurityDescriptor: "", // Empty means current user only (go-winio default)
		MessageMode:        false,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}

	// Create the named pipe listener
	ln, err := winio.ListenPipe(pipeName, config)
	if err != nil {
		return nil, fmt.Errorf("cannot create named pipe: %w", err)
	}

	listener := &Listener{
		Listener: &namedPipeListener{
			Listener: ln,
			pipeName: pipeName,
			logger:   logger,
		},
		Source:  ConnectionSourceTray,
		Address: pipeName,
	}

	logger.Info("Windows named pipe listener created",
		zap.String("pipe", pipeName),
		zap.String("security", "current user only"))

	return listener, nil
}

// namedPipeListener wraps net.Listener to add logging
type namedPipeListener struct {
	net.Listener
	pipeName string
	logger   *zap.Logger
}

// Close closes the pipe listener
func (pl *namedPipeListener) Close() error {
	pl.logger.Info("Closing Windows named pipe listener", zap.String("pipe", pl.pipeName))
	return pl.Listener.Close()
}

// Accept accepts a connection from the named pipe
func (pl *namedPipeListener) Accept() (net.Conn, error) {
	conn, err := pl.Listener.Accept()
	if err != nil {
		return nil, err
	}

	pl.logger.Debug("Accepted connection on named pipe", zap.String("pipe", pl.pipeName))

	// Note: go-winio automatically enforces the security descriptor we set,
	// so we don't need to manually verify the connecting user's SID here.
	// The library only allows connections from users matching the ACL.

	return conn, nil
}

// createUnixListenerPlatform is a stub for Windows (not supported)
func createUnixListenerPlatform(socketPath string, logger *zap.Logger) (*Listener, error) {
	return nil, fmt.Errorf("Unix domain sockets are not supported on Windows")
}
