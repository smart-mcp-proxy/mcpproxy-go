package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 132: a sign-in request names a client and the label it reported, and
// its decision tells which request was approved. Only an administrator sees
// either; a scoped caller never does.
func TestSSE_OAuthLoginEventsAreAdminOnly(t *testing.T) {
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "bot", AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
	admin := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	for _, evt := range []internalRuntime.Event{
		{Type: internalRuntime.EventTypeOAuthAuthorizationPending,
			Payload: map[string]any{"request_id": "r1", "client_id": "cursor", "client_label": "Cursor IDE"}},
		{Type: internalRuntime.EventTypeOAuthAuthorizationDecided,
			Payload: map[string]any{"request_id": "r1", "status": "approved"}},
	} {
		require.False(t, eventVisibleToCaller(scoped, evt), "%s must be dropped for a scoped caller", evt.Type)
		require.True(t, eventVisibleToCaller(admin, evt), "%s reaches an administrator", evt.Type)
	}
}
