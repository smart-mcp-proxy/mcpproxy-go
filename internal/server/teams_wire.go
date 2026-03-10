//go:build teams

package server

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams"
)

// wireTeamsOAuth sets up teams multi-user OAuth routes on the HTTP API server.
// This is called during server initialization after the HTTP API server is created.
func wireTeamsOAuth(s *Server, httpAPIServer *httpapi.Server) {
	cfg := s.runtime.Config()
	if cfg == nil {
		s.logger.Debug("Teams OAuth wiring skipped: no config available")
		return
	}

	sm := s.runtime.StorageManager()
	if sm == nil {
		s.logger.Debug("Teams OAuth wiring skipped: no storage manager available")
		return
	}

	deps := teams.Dependencies{
		Router:            httpAPIServer.Router(),
		DB:                sm.GetDB(),
		Logger:            s.logger.Sugar(),
		Config:            cfg,
		DataDir:           cfg.DataDir,
		ManagementService: s.runtime.GetManagementService(),
		StorageManager:    sm,
	}

	if err := teams.SetupAll(deps); err != nil {
		s.logger.Error("Failed to initialize teams features", zap.Error(err))
	}
}
