package telemetry

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// EffectiveStateSource names where the effective telemetry state came from.
type EffectiveStateSource string

const (
	// StateSourceEnv: an environment variable (DO_NOT_TRACK, CI or
	// MCPPROXY_TELEMETRY=false) disabled telemetry.
	StateSourceEnv EffectiveStateSource = "env"
	// StateSourceConfig: telemetry.enabled is set in the config file (true or
	// false).
	StateSourceConfig EffectiveStateSource = "config"
	// StateSourceDefault: telemetry.enabled is unset, which resolves to on.
	StateSourceDefault EffectiveStateSource = "default"
)

// EffectiveState is the resolved telemetry state served on
// GET /api/v1/status (Spec 109 FR-044a). Enabled always equals
// EffectiveTelemetryEnabled for the same config. DisabledBy is set only when
// Source is StateSourceEnv and reuses the EnvDisabledReason vocabulary.
//
// Dev (non-semver) builds also never transmit; that is a build property, not a
// user setting, so it is deliberately not folded into Enabled.
type EffectiveState struct {
	Enabled    bool                 `json:"enabled"`
	Source     EffectiveStateSource `json:"source"`
	DisabledBy EnvDisabledReason    `json:"disabled_by,omitempty"`
}

// ResolveEffectiveState reports whether and why telemetry is on for cfg. It
// reads cfg.Telemetry.Enabled directly rather than cfg.IsTelemetryEnabled():
// that helper's env check is a strict string compare that differs from
// IsDisabledByEnv's trim and case-fold, and the env branch here already covers
// every env case.
func ResolveEffectiveState(cfg *config.Config) EffectiveState {
	if disabled, reason := IsDisabledByEnv(); disabled {
		return EffectiveState{Enabled: false, Source: StateSourceEnv, DisabledBy: reason}
	}
	if cfg == nil {
		return EffectiveState{Enabled: false, Source: StateSourceDefault}
	}
	if cfg.Telemetry == nil || cfg.Telemetry.Enabled == nil {
		return EffectiveState{Enabled: true, Source: StateSourceDefault}
	}
	return EffectiveState{Enabled: *cfg.Telemetry.Enabled, Source: StateSourceConfig}
}
