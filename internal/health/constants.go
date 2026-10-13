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

// Status values - the ONE vocabulary every surface (Web UI, macOS, tray, CLI)
// renders as text (Spec 109 FR-010/FR-011). Level stays a severity signal for
// badges/tray coloring only; no renderer may print Level as text.
const (
	StatusReady          = "ready"
	StatusConnecting     = "connecting"
	StatusSignInRequired = "sign_in_required"
	StatusNeedsReview    = "needs_review"
	StatusNeedsSecret    = "needs_secret"
	StatusNeedsConfig    = "needs_config"
	StatusError          = "error"
	StatusDisabled       = "disabled"
)

// ActionPriority is the fixed cross-surface priority order for entries in
// HealthStatus.Actions (Spec 109 FR-012). Every branch
// of CalculateHealth already emits its Actions slice in this order; it is
// exported so other code (tests, generators) can assert on it without
// duplicating the literal order.
var ActionPriority = []string{
	ActionLogin, ActionSetSecret, ActionConfigure, ActionEditURL,
	ActionApprove, ActionRestart, ActionViewLogs, ActionEnable,
}

// StatusOrder lists every Status* value in the priority order Spec 109
// documents them. Used to iterate StatusLabels deterministically (e.g. code gen).
var StatusOrder = []string{
	StatusReady, StatusConnecting, StatusSignInRequired, StatusNeedsReview,
	StatusNeedsSecret, StatusNeedsConfig, StatusError, StatusDisabled,
}

// StatusLabels is the one label table for `status`, binding for the Web UI,
// the macOS window and tray, and the CLI table (Spec 109 FR-014).
var StatusLabels = map[string]string{
	StatusReady:          "Online",
	StatusConnecting:     "Connecting",
	StatusSignInRequired: "Sign-in required",
	StatusNeedsReview:    "Needs review",
	StatusNeedsSecret:    "Secret required",
	StatusNeedsConfig:    "Needs configuration",
	StatusError:          "Error",
	StatusDisabled:       "Disabled",
}

// ActionLabels is the one label table for a primary button keyed on
// Actions[0] (Spec 109 FR-014).
var ActionLabels = map[string]string{
	ActionLogin:     "Sign in",
	ActionSetSecret: "Add secret",
	ActionConfigure: "Fix config",
	ActionEditURL:   "Edit URL",
	ActionApprove:   "Review",
	ActionRestart:   "Restart",
	ActionViewLogs:  "View logs",
	ActionEnable:    "Enable",
}

// StatusLabel returns the cross-surface label for a status value, or the raw
// value itself if it is not recognized (defensive default; every value
// CalculateHealth emits is a key of StatusLabels).
func StatusLabel(status string) string {
	if label, ok := StatusLabels[status]; ok {
		return label
	}
	return status
}

// ActionLabel returns the cross-surface button label for an action value, or
// "" for ActionNone / an unrecognized value.
func ActionLabel(action string) string {
	return ActionLabels[action]
}

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
