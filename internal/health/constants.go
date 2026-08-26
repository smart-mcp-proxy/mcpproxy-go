// Package health provides unified health status calculation for upstream MCP servers.
//
// IMPORTANT: These constants are mirrored in TypeScript. When adding or modifying
// health levels, admin states, or actions, update cmd/generate-types/main.go and
// regenerate frontend/src/types/contracts.ts by running: go run ./cmd/generate-types
package health

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"

// Health levels
const (
	LevelHealthy   = "healthy"
	LevelDegraded  = "degraded"
	LevelUnhealthy = "unhealthy"
)

// Admin states
const (
	StateEnabled     = "enabled"
	StateDisabled    = "disabled"
	StateQuarantined = "quarantined"
)

// Actions - suggested remediation for health issues
const (
	ActionNone      = ""
	ActionLogin     = "login"
	ActionRestart   = "restart"
	ActionEnable    = "enable"
	ActionApprove   = "approve"
	ActionViewLogs  = "view_logs"
	ActionSetSecret = "set_secret"
	ActionConfigure = "configure"
	// ActionEditURL is offered when the endpoint itself is unusable — the host
	// does not resolve, the scheme is unsupported, or the URL is malformed.
	// Restarting cannot fix any of those; editing the address can.
	ActionEditURL = "edit_url"
)

// IsHealthy returns true if the server is considered healthy.
// It uses health.level as the source of truth, with a fallback to the legacy
// connected field for backward compatibility when health is nil.
func IsHealthy(health *contracts.HealthStatus, legacyConnected bool) bool {
	if health != nil {
		return health.Level == LevelHealthy
	}
	// Fallback to legacy connected field if health is not available
	return legacyConnected
}
