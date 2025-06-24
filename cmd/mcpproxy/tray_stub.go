//go:build nogui || headless || linux

package main

import (
	"context"

	"go.uber.org/zap"

	"mcpproxy-go/internal/server"
)

// StubTray is a no-op implementation of TrayInterface for headless/Linux builds
type StubTray struct {
	logger *zap.SugaredLogger
}

// Run implements TrayInterface but does nothing for headless/Linux builds
func (s *StubTray) Run(ctx context.Context) error {
	s.logger.Info("Tray functionality disabled (nogui/headless/linux build)")
	<-ctx.Done()
	return ctx.Err()
}

// createTray creates a stub tray implementation for headless/Linux platforms
func createTray(_ *server.Server, logger *zap.SugaredLogger, _ string, _ func()) TrayInterface {
	return &StubTray{
		logger: logger,
	}
}
