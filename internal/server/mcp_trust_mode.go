package server

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
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

// validateAnnotationOverridesForAdd validates annotation_overrides on the MCP
// add door (deep-copied already, nils dropped). It mirrors the REST PATCH
// write gate (handlePatchServer): per-key name check via
// config.IsValidToolNameForOverride plus a temp-config ValidateDetailed
// filtered to annotation_overrides errors. Returns a 400-style MCP error
// result on invalid input, nil when valid.
func validateAnnotationOverridesForAdd(serverName string, overrides map[string]*config.ToolAnnotations) *mcp.CallToolResult {
	for k := range overrides {
		if k != "*" && !config.IsValidToolNameForOverride(k) {
			return mcp.NewToolResultError(fmt.Sprintf("invalid annotation_overrides[%q]: invalid tool name (use \"*\" or alphanumeric._:-)", k))
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	tmp := &config.Config{Servers: []*config.ServerConfig{{Name: serverName, AnnotationOverrides: overrides}}}
	for _, e := range tmp.ValidateDetailed() {
		if strings.Contains(e.Field, "annotation_overrides") {
			return mcp.NewToolResultError(e.Error())
		}
	}
	return nil
}
