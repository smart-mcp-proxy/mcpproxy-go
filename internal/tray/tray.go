package tray

import (
	"context"

	"go.uber.org/zap"
)

// App represents the system tray application
type App struct {
	server interface{} // placeholder for server interface
	logger *zap.SugaredLogger
}

// New creates a new tray application
func New(server interface{}, logger *zap.SugaredLogger) *App {
	return &App{
		server: server,
		logger: logger,
	}
}

// Run starts the system tray application
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("System tray functionality not yet implemented")

	// For now, just wait for context cancellation
	<-ctx.Done()
	return ctx.Err()
}
