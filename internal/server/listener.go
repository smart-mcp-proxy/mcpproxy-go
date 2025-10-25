package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go.uber.org/zap"

	"mcpproxy-go/internal/transport"
)

// Re-export transport types for backward compatibility
type ConnectionSource = transport.ConnectionSource

const (
	ConnectionSourceTCP  = transport.ConnectionSourceTCP
	ConnectionSourceTray = transport.ConnectionSourceTray
)

// ListenerConfig contains configuration for creating listeners
type ListenerConfig struct {
	// DataDir is the data directory where socket file will be created
	DataDir string

	// TrayEndpoint is an optional explicit override for the tray endpoint
	// Format: "unix:///path/to/socket.sock" or "npipe:////./pipe/name"
	TrayEndpoint string

	// TCPAddress is the address for the TCP listener (for browsers)
	// Format: "127.0.0.1:8080" or ":8080"
	TCPAddress string

	// Logger for diagnostic output
	Logger *zap.Logger
}

// Listener wraps a net.Listener with metadata about its source
type Listener struct {
	net.Listener
	Source  ConnectionSource
	Address string // Display address for logging
}

// ListenerManager manages multiple listeners (TCP + Tray socket/pipe)
type ListenerManager struct {
	config    *ListenerConfig
	logger    *zap.Logger
	listeners []*Listener
}

// NewListenerManager creates a new listener manager
func NewListenerManager(config *ListenerConfig) *ListenerManager {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}

	return &ListenerManager{
		config:    config,
		logger:    config.Logger,
		listeners: make([]*Listener, 0, 2),
	}
}

// CreateTCPListener creates a TCP listener for browser/remote access
func (m *ListenerManager) CreateTCPListener() (*Listener, error) {
	if m.config.TCPAddress == "" {
		m.logger.Debug("No TCP address configured, skipping TCP listener")
		return nil, nil
	}

	m.logger.Info("Creating TCP listener", zap.String("address", m.config.TCPAddress))

	ln, err := net.Listen("tcp", m.config.TCPAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create TCP listener: %w", err)
	}

	listener := &Listener{
		Listener: ln,
		Source:   ConnectionSourceTCP,
		Address:  ln.Addr().String(),
	}

	m.listeners = append(m.listeners, listener)
	m.logger.Info("TCP listener created", zap.String("address", listener.Address))

	return listener, nil
}

// CreateTrayListener creates a Unix socket (macOS/Linux) or named pipe (Windows) listener for tray access
func (m *ListenerManager) CreateTrayListener() (*Listener, error) {
	// Determine endpoint based on configuration
	endpoint := m.config.TrayEndpoint
	if endpoint == "" {
		// Auto-detect based on data directory and platform
		endpoint = m.getDefaultTrayEndpoint()
	}

	if endpoint == "" {
		m.logger.Debug("No tray endpoint configured, skipping tray listener")
		return nil, nil
	}

	m.logger.Info("Creating tray listener", zap.String("endpoint", endpoint))

	// Parse endpoint scheme
	if strings.HasPrefix(endpoint, "unix://") {
		return m.createUnixListener(strings.TrimPrefix(endpoint, "unix://"))
	} else if strings.HasPrefix(endpoint, "npipe://") {
		return m.createNamedPipeListener(strings.TrimPrefix(endpoint, "npipe://"))
	}

	return nil, fmt.Errorf("unsupported tray endpoint scheme: %s (expected unix:// or npipe://)", endpoint)
}

// getDefaultTrayEndpoint returns the default tray endpoint based on platform and data directory
func (m *ListenerManager) getDefaultTrayEndpoint() string {
	if runtime.GOOS == "windows" {
		// Windows: Named pipe
		username := os.Getenv("USERNAME")
		if username == "" {
			username = "default"
		}
		return fmt.Sprintf("npipe:////./pipe/mcpproxy-%s", username)
	}

	// Unix: Socket in data directory
	if m.config.DataDir == "" {
		m.logger.Warn("No data directory configured, cannot determine default socket path")
		return ""
	}

	socketPath := filepath.Join(m.config.DataDir, "mcpproxy.sock")
	return fmt.Sprintf("unix://%s", socketPath)
}

// createUnixListener creates a Unix domain socket listener (macOS/Linux)
func (m *ListenerManager) createUnixListener(socketPath string) (*Listener, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("unix domain sockets not supported on Windows")
	}

	// This will be implemented in listener_unix.go
	return createUnixListenerPlatform(socketPath, m.logger)
}

// createNamedPipeListener creates a Windows named pipe listener
func (m *ListenerManager) createNamedPipeListener(pipeName string) (*Listener, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("named pipes only supported on Windows")
	}

	// This will be implemented in listener_windows.go
	return createNamedPipeListenerPlatform(pipeName, m.logger)
}

// CloseAll closes all managed listeners
func (m *ListenerManager) CloseAll() error {
	m.logger.Info("Closing all listeners", zap.Int("count", len(m.listeners)))

	var firstErr error
	for _, ln := range m.listeners {
		m.logger.Debug("Closing listener", zap.String("source", string(ln.Source)), zap.String("address", ln.Address))

		if err := ln.Close(); err != nil && firstErr == nil {
			firstErr = err
			m.logger.Error("Failed to close listener", zap.Error(err), zap.String("address", ln.Address))
		}
	}

	m.listeners = nil
	return firstErr
}

// GetListeners returns all active listeners
func (m *ListenerManager) GetListeners() []*Listener {
	return m.listeners
}

// ValidateDataDirectory checks that the data directory has secure permissions
// This is called before creating socket listeners to ensure security
func ValidateDataDirectory(dataDir string, logger *zap.Logger) error {
	if dataDir == "" {
		return fmt.Errorf("data directory not specified")
	}

	// Expand ~ if present
	if strings.HasPrefix(dataDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot expand home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[2:])
	}

	logger.Info("Validating data directory security", zap.String("path", dataDir))

	// Check if directory exists
	info, err := os.Stat(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to create with secure permissions
			logger.Info("Data directory does not exist, creating with secure permissions", zap.String("path", dataDir))
			if err := os.MkdirAll(dataDir, 0700); err != nil {
				return fmt.Errorf("cannot create data directory: %w", err)
			}
			return nil // Created with secure permissions
		}
		return fmt.Errorf("cannot access data directory: %w", err)
	}

	// Check it's a directory
	if !info.IsDir() {
		return fmt.Errorf("data path exists but is not a directory: %s", dataDir)
	}

	// Platform-specific permission checks
	return validateDataDirectoryPermissionsPlatform(dataDir, info, logger)
}

// TagConnectionContext tags a context with the connection source
// TagConnectionContext wraps transport.TagConnectionContext for backward compatibility
func TagConnectionContext(ctx context.Context, source ConnectionSource) context.Context {
	return transport.TagConnectionContext(ctx, source)
}

// GetConnectionSource wraps transport.GetConnectionSource for backward compatibility
func GetConnectionSource(ctx context.Context) ConnectionSource {
	return transport.GetConnectionSource(ctx)
}
