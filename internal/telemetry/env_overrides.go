package telemetry

import (
	"os"
	"strings"
)

// EnvDisabledReason explains why telemetry was disabled by an environment
// variable, if any. The empty string means env did not disable telemetry.
type EnvDisabledReason string

const (
	EnvDisabledNone         EnvDisabledReason = ""
	EnvDisabledByDoNotTrack EnvDisabledReason = "DO_NOT_TRACK"
	EnvDisabledByCI         EnvDisabledReason = "CI"
	EnvDisabledByMCPProxy   EnvDisabledReason = "MCPPROXY_TELEMETRY=false"
)

// IsDisabledByEnv evaluates the env var precedence chain for telemetry
// disablement. Precedence (highest first):
//  1. DO_NOT_TRACK set to any non-empty, non-"0" value (consoledonottrack.com)
//  2. CI=true or CI=1
//  3. MCPPROXY_TELEMETRY=false
//
// Returns true and the reason if telemetry should be disabled.
func IsDisabledByEnv() (bool, EnvDisabledReason) {
	if v := strings.TrimSpace(os.Getenv("DO_NOT_TRACK")); v != "" && v != "0" {
		return true, EnvDisabledByDoNotTrack
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("CI"))); v == "true" || v == "1" {
		return true, EnvDisabledByCI
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MCPPROXY_TELEMETRY")), "false") {
		return true, EnvDisabledByMCPProxy
	}
	return false, EnvDisabledNone
}
