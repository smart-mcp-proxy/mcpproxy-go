//go:build !nogui && !headless && !linux

package main

import (
	"go.uber.org/zap"

	"mcpproxy-go/internal/server"
	"mcpproxy-go/internal/tray"
)

// createTray creates a new tray application for GUI platforms
func createTray(srv *server.Server, logger *zap.SugaredLogger, version string, shutdownFunc func()) TrayInterface {
	return tray.New(srv, logger, version, shutdownFunc)
}
