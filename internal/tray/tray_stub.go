//go:build nogui || headless

package tray

import (
	"context"

	"go.uber.org/zap"
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
}

// App represents the system tray application (stub version)
type App struct {
	logger *zap.SugaredLogger
}

// New creates a new tray application (stub version)
func New(server ServerInterface, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	return &App{
		logger: logger,
	}
}

// Run starts the system tray application (stub version - does nothing)
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Tray functionality disabled (nogui/headless build)")
	<-ctx.Done()
	return ctx.Err()
}
