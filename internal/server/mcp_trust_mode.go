package server

import (
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// invalidTrustModeError renders the operator-facing error for an unrecognized
// trust_mode passed to the upstream_servers tool (GH #938). It names both the
// offending value and the accepted vocabulary; matching is case-sensitive
// because EffectiveTrustMode() fails closed to manual on anything else, so
// accepting "Scan" would leave a typo looking like an enabled scan tier.
func invalidTrustModeError(mode string) string {
	return fmt.Sprintf("invalid trust_mode %q: must be one of: %s (values are case-sensitive; omit the field to leave it unchanged)",
		mode, strings.Join(config.ValidTrustModes(), ", "))
}
