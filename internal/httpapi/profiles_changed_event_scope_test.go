package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f T075 (FR-038): profiles.changed names profiles (and so servers and
// policy) a scoped caller may not reach, so it is administrator-only on SSE,
// like client.binding_changed - which stays administrator-only when a bulk
// assign emits it per moved client.
func TestSSE_ProfilesChangedIsAdminOnly(t *testing.T) {
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "bot", AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
	admin := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})

	for _, evt := range []internalRuntime.Event{
		{Type: internalRuntime.EventTypeProfilesChanged, Payload: map[string]any{"name": "work-full", "change": "update"}},
		{Type: internalRuntime.EventTypeProfilesChanged, Payload: map[string]any{"name": "work-ro", "change": "create"}},
		{Type: internalRuntime.EventTypeProfilesChanged, Payload: map[string]any{"name": "", "change": "anonymous"}},
		// A bulk assign emits one client.binding_changed per moved client.
		{Type: internalRuntime.EventTypeClientBindingChanged, Payload: map[string]any{"client_id": "cursor", "token_name": "client-cursor", "profile": "full", "previous_profile": "ro", "mode": "locked"}},
	} {
		require.False(t, eventVisibleToCaller(scoped, evt), "a scoped subscriber never receives %s", evt.Type)
		require.True(t, eventVisibleToCaller(admin, evt), "an administrator receives %s", evt.Type)
		require.True(t, eventVisibleToCaller(context.Background(), evt), "the socket (no AuthContext) receives %s", evt.Type)
	}

	// Even a payload with no name at all cannot leak past the type-level rule.
	require.False(t, eventVisibleToCaller(scoped, internalRuntime.Event{Type: internalRuntime.EventTypeProfilesChanged}))
}
