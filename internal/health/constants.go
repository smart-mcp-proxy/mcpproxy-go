// Package health provides unified health status calculation for upstream MCP servers.
package health

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
	ActionNone     = ""
	ActionLogin    = "login"
	ActionRestart  = "restart"
	ActionEnable   = "enable"
	ActionApprove  = "approve"
	ActionViewLogs = "view_logs"
)
