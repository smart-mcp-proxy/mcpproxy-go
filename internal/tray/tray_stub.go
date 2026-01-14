//go:build nogui || headless || linux

package tray

import (
	"context"

	"go.uber.org/zap"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// ServerInterface defines the interface for server control (stub version)
type ServerInterface interface {
	IsRunning() bool
	GetListenAddress() string
	GetUpstreamStats() map[string]interface{}
	StartServer(ctx context.Context) error
	StopServer() error
	GetStatus() interface{}
	StatusChannel() <-chan interface{}
	EventsChannel() <-chan internalRuntime.Event

	// Quarantine management methods
	GetQuarantinedServers() ([]map[string]interface{}, error)
	UnquarantineServer(serverName string) error

	// Server management methods for tray menu
	EnableServer(serverName string, enabled bool) error
	QuarantineServer(serverName string, quarantined bool) error
	GetAllServers() ([]map[string]interface{}, error)
	SetListenAddress(addr string, persist bool) error
	SuggestAlternateListen(baseAddr string) (string, error)

	// Config management for file watching
	ReloadConfiguration() error
	GetConfigPath() string
	GetLogDir() string
}

// App represents the system tray application (stub version)
type App struct {
	logger *zap.SugaredLogger
}

// New creates a new tray application (stub version)
func New(_ ServerInterface, logger *zap.SugaredLogger, _ string, _ func()) *App {
	return &App{
		logger: logger,
	}
}

// NewWithAPIClient creates a new tray application with an API client (stub version)
func NewWithAPIClient(_ ServerInterface, _ interface{ OpenWebUI() error }, logger *zap.SugaredLogger, _ string, _ func()) *App {
	return &App{
		logger: logger,
	}
}

// SetConnectionState updates the tray's connection state (stub version - does nothing)
func (a *App) SetConnectionState(_ ConnectionState) {
	// No-op in stub version
}

// Run starts the system tray application (stub version - does nothing)
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Tray functionality disabled (nogui/headless build)")
	<-ctx.Done()
	return ctx.Err()
}

// Quit stops the system tray application (stub version - does nothing)
func (a *App) Quit() {
	// No-op in stub version
}
