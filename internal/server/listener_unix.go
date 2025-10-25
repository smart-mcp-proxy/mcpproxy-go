//go:build (linux || darwin) && !windows

package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// createUnixListenerPlatform creates a Unix domain socket listener
// This is the Unix/Linux/macOS implementation
func createUnixListenerPlatform(socketPath string, logger *zap.Logger) (*Listener, error) {
	logger.Info("Creating Unix domain socket listener", zap.String("path", socketPath))

	// Ensure parent directory exists with secure permissions
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create socket directory: %w", err)
	}

	// Clean up stale socket if it exists
	if err := cleanupStaleSocket(socketPath, logger); err != nil {
		return nil, fmt.Errorf("cannot cleanup stale socket: %w", err)
	}

	// Create the Unix socket listener
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create Unix socket: %w", err)
	}

	// Set secure permissions on the socket file (user read/write only)
	if err := os.Chmod(socketPath, 0600); err != nil {
		ln.Close()
		os.Remove(socketPath)
		return nil, fmt.Errorf("cannot set socket permissions: %w", err)
	}

	// Verify ownership
	if err := verifySocketOwnership(socketPath, logger); err != nil {
		ln.Close()
		os.Remove(socketPath)
		return nil, fmt.Errorf("socket ownership verification failed: %w", err)
	}

	listener := &Listener{
		Listener: &unixListener{
			Listener:   ln,
			socketPath: socketPath,
			logger:     logger,
		},
		Source:  ConnectionSourceTray,
		Address: socketPath,
	}

	logger.Info("Unix domain socket listener created",
		zap.String("path", socketPath),
		zap.String("permissions", "0600"))

	return listener, nil
}

// cleanupStaleSocket removes a stale socket file left by a crashed process
func cleanupStaleSocket(socketPath string, logger *zap.Logger) error {
	// Check if socket file exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return nil // No cleanup needed
	}

	logger.Info("Socket file exists, checking if stale", zap.String("path", socketPath))

	// Try to connect to the socket with a short timeout
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err == nil {
		// Socket is active, another process is using it
		conn.Close()
		return fmt.Errorf("socket is in use by another process")
	}

	// Socket exists but not accepting connections → stale socket
	logger.Info("Removing stale socket file", zap.String("path", socketPath))
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("cannot remove stale socket: %w", err)
	}

	return nil
}

// verifySocketOwnership checks that the socket is owned by the current user
func verifySocketOwnership(socketPath string, logger *zap.Logger) error {
	info, err := os.Stat(socketPath)
	if err != nil {
		return fmt.Errorf("cannot stat socket: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot get socket ownership info")
	}

	currentUID := uint32(os.Getuid())
	if stat.Uid != currentUID {
		return fmt.Errorf("socket not owned by current user (uid=%d, expected=%d)", stat.Uid, currentUID)
	}

	logger.Debug("Socket ownership verified",
		zap.String("path", socketPath),
		zap.Uint32("uid", stat.Uid))

	return nil
}

// unixListener wraps net.Listener to add cleanup on close
type unixListener struct {
	net.Listener
	socketPath string
	logger     *zap.Logger
}

// Close closes the listener and removes the socket file
func (ul *unixListener) Close() error {
	ul.logger.Info("Closing Unix domain socket listener", zap.String("path", ul.socketPath))

	// Close the listener first
	err := ul.Listener.Close()

	// Remove the socket file
	if removeErr := os.Remove(ul.socketPath); removeErr != nil {
		ul.logger.Warn("Failed to remove socket file", zap.Error(removeErr), zap.String("path", ul.socketPath))
	} else {
		ul.logger.Debug("Socket file removed", zap.String("path", ul.socketPath))
	}

	return err
}

// Accept accepts a connection and verifies the connecting UID
func (ul *unixListener) Accept() (net.Conn, error) {
	conn, err := ul.Listener.Accept()
	if err != nil {
		return nil, err
	}

	// Verify connecting UID matches our UID
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("not a Unix connection")
	}

	// Get peer credentials (UID/GID)
	file, err := unixConn.File()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot get connection file descriptor: %w", err)
	}
	defer file.Close()

	// Get peer credentials using SO_PEERCRED (Linux) or LOCAL_PEERCRED (BSD/macOS)
	ucred, err := getPeerCredentials(int(file.Fd()))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot get peer credentials: %w", err)
	}

	currentUID := uint32(os.Getuid())
	if ucred.Uid != currentUID {
		conn.Close()
		ul.logger.Warn("Rejected connection from different user",
			zap.Uint32("peer_uid", ucred.Uid),
			zap.Uint32("expected_uid", currentUID))
		return nil, fmt.Errorf("connection from different user (uid=%d, expected=%d)", ucred.Uid, currentUID)
	}

	ul.logger.Debug("Accepted connection with verified UID",
		zap.Uint32("uid", ucred.Uid),
		zap.Uint32("gid", ucred.Gid),
		zap.Int32("pid", ucred.Pid))

	return conn, nil
}

// Ucred holds Unix credentials
type Ucred struct {
	Pid int32
	Uid uint32
	Gid uint32
}

// getPeerCredentials gets the credentials of the peer connected to the socket
// Platform-specific implementations in listener_linux.go and listener_darwin.go
func getPeerCredentials(fd int) (*Ucred, error) {
	return getPeerCredentialsPlatform(fd)
}

// createNamedPipeListenerPlatform is a stub for Unix platforms (not supported)
func createNamedPipeListenerPlatform(pipeName string, logger *zap.Logger) (*Listener, error) {
	return nil, fmt.Errorf("named pipes are only supported on Windows")
}
