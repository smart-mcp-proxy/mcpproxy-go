package main

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// classifyStartupError maps an error from the serve startup path to a Spec
// 042 startup-outcome enum value. The mapping mirrors classifyError() / exit
// codes so the telemetry signal aligns with the user-visible exit code.
func classifyStartupError(err error) string {
	switch classifyError(err) {
	case ExitCodePortConflict:
		return "port_conflict"
	case ExitCodeDBLocked:
		return "db_locked"
	case ExitCodeConfigError:
		return "config_error"
	case ExitCodePermissionError:
		return "permission_error"
	case ExitCodeSuccess:
		return "success"
	default:
		return "other_error"
	}
}

// recordStartupOutcome persists the last startup outcome to the config file.
// Spec 042 User Story 5. The next heartbeat reads this value into the payload
// as last_startup_outcome.
func recordStartupOutcome(cfg *config.Config, configPath, outcome string) {
	if cfg == nil {
		return
	}
	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}
	if cfg.Telemetry.LastStartupOutcome == outcome {
		return
	}
	cfg.Telemetry.LastStartupOutcome = outcome
	if configPath == "" {
		return
	}
	if err := config.SaveConfig(cfg, configPath); err != nil {
		// Best-effort; telemetry must never block startup.
		zap.L().Debug("Failed to persist last_startup_outcome",
			zap.String("outcome", outcome),
			zap.Error(err))
	}
}
