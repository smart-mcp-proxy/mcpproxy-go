package telemetry

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// trustModeKeys is the canonical fixed-enum set of trust-tier labels emitted
// in trust_mode_distribution. Dashboard queries can rely on all three keys
// always being present (even at zero), the same convention as protocolKeys.
// It mirrors config.ValidTrustModes(); the fixed list lives here so a future
// widening of the config enum is a deliberate telemetry change rather than a
// silent cardinality increase.
var trustModeKeys = []string{
	string(config.TrustModeAuto),
	string(config.TrustModeScan),
	string(config.TrustModeManual),
}

// IsTrustModeKey reports whether key is a member of the fixed trust-tier enum
// permitted in the heartbeat's trust_mode_distribution map.
func IsTrustModeKey(key string) bool {
	for _, k := range trustModeKeys {
		if k == key {
			return true
		}
	}
	return false
}

// buildTrustModeDistribution counts configured upstream servers grouped by
// their EFFECTIVE trust tier (config.ServerConfig.EffectiveTrustMode — the
// single resolution point, which folds the empty/inherit case, a typo'd mode,
// and the legacy auto_approve_tool_changes / skip_quarantine fields into one
// of auto|scan|manual).
//
// Anonymity: counts only, keyed exclusively by the fixed enum. Server names,
// URLs, and raw config strings never reach the map — a mode outside the enum
// is impossible by construction (EffectiveTrustMode fails closed to manual),
// but an unexpected value is dropped here rather than emitted.
func buildTrustModeDistribution(cfg *config.Config) map[string]int {
	counts := make(map[string]int, len(trustModeKeys))
	for _, k := range trustModeKeys {
		counts[k] = 0
	}
	if cfg == nil {
		return counts
	}
	for _, srv := range cfg.Servers {
		if srv == nil {
			continue
		}
		key := string(srv.EffectiveTrustMode())
		if !IsTrustModeKey(key) {
			// Unreachable today (EffectiveTrustMode returns one of the three);
			// dropped rather than emitted so a future enum widening cannot
			// leak an unbounded key into the payload.
			continue
		}
		counts[key]++
	}
	return counts
}
