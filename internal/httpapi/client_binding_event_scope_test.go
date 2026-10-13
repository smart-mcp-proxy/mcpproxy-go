package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestSSE_ClientBindingChangedIsAdminOnly pins plan D19: the event discloses
// bindings, so a scoped caller never sees it while an administrator does.
func TestSSE_ClientBindingChangedIsAdminOnly(t *testing.T) {
	evt := internalRuntime.Event{
		Type:    internalRuntime.EventTypeClientBindingChanged,
		Payload: map[string]any{"client_id": "cursor", "token_name": "client-cursor", "profile": "ro"},
	}
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "bot", AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
	require.False(t, eventVisibleToCaller(scoped, evt), "a scoped caller must not see binding changes")

	admin := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	require.True(t, eventVisibleToCaller(admin, evt))
	require.True(t, eventVisibleToCaller(context.Background(), evt), "no AuthContext is unrestricted (socket / in-process)")
}
