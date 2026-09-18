//go:build server

package server

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// wireServerEditionOAuth sets up server edition multi-user OAuth routes on the HTTP API server.
// This is called during server initialization after the HTTP API server is created.
func wireServerEditionOAuth(s *Server, httpAPIServer *httpapi.Server) {
	cfg := s.runtime.Config()
	if cfg == nil {
		s.logger.Debug("Server OAuth wiring skipped: no config available")
		return
	}

	sm := s.runtime.StorageManager()
	if sm == nil {
		s.logger.Debug("Server OAuth wiring skipped: no storage manager available")
		return
	}

	deps := serveredition.Dependencies{
		Router:  httpAPIServer.Router(),
		DB:      sm.GetDB(),
		Logger:  s.logger.Sugar(),
		Config:  cfg,
		DataDir: cfg.DataDir,
		// Runtime.Config() reads the current snapshot through the config
		// service's atomic pointer, so this stays correct across hot reloads —
		// which is load-bearing for the server edition's name-collision and
		// entitlement checks. See Dependencies.ConfigProvider.
		ConfigProvider:    func() *config.Config { return s.runtime.Config() },
		SetServerShared:   s.runtime.SetServerShared,
		ManagementService: s.runtime.GetManagementService(),
		StorageManager:    sm,

		// Spec 107 T084: setup.go builds the session-principal resolver and
		// hands it back here so it can be installed on the same
		// httpapi.Server that serves /api/v1.
		InstallSessionPrincipalResolver: httpAPIServer.SetSessionPrincipalResolver,

		// Spec 107 T086: the same convert+mask composition core GET /activity
		// applies, for GET /api/v1/user/activity to reuse.
		ProjectActivity: httpAPIServer.ActivityProjector(),
	}

	if err := serveredition.SetupAll(deps); err != nil {
		s.logger.Error("Failed to initialize server features", zap.Error(err))
	}

	// Spec 107 (US7, telemetry v13): install the user counter behind
	// member_count_bucket. Only a count crosses this seam — the closure reads
	// the user store and returns len(); no user record, email, group or IdP
	// subject is ever handed to telemetry. Installed regardless of SetupAll's
	// outcome and of whether the block is enabled: a server-edition binary
	// with the block off reports "0" from an empty bucket, which is the same
	// value the personal edition reports with no counter at all. nil-safe on
	// the telemetry side (short-lived CLI commands have no service).
	if ts := s.runtime.TelemetryService(); ts != nil {
		userStore := users.NewUserStore(sm.GetDB())
		ts.SetUserCounter(func() (int, error) {
			list, err := userStore.ListUsers()
			if err != nil {
				return 0, err
			}
			return len(list), nil
		})
	}
}
