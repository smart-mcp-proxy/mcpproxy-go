//go:build server

package server

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// editionToolCallAttributes adds multi-tenant identity to tool-call spans in the
// server edition (MCP-32): the authenticated user_id and the active profile
// slug. These are span attributes (per-request) rather than Prometheus labels,
// which keeps metric cardinality bounded while still letting operators slice
// traces by tenant/profile.
func editionToolCallAttributes(ctx context.Context, profileSlug string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 2)
	if ac := auth.AuthContextFromContext(ctx); ac != nil && ac.IsUser() {
		if uid := ac.GetUserID(); uid != "" {
			attrs = append(attrs, attribute.String("user_id", uid))
		}
	}
	if profileSlug != "" {
		attrs = append(attrs, attribute.String("profile", profileSlug))
	}
	return attrs
}
