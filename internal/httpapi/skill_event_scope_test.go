package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestSSE_SkillQuarantineChangeIsAdminOnly pins Spec 125 FR-019a: the event
// names a skill that may be unapproved, so a scoped caller never sees it, not
// even for a server it may see.
func TestSSE_SkillQuarantineChangeIsAdminOnly(t *testing.T) {
	evt := internalRuntime.Event{
		Type:    internalRuntime.EventTypeActivitySkillQuarantineChange,
		Payload: map[string]any{"server_name": "alpha", "skill": "pdf", "action": "held_changed"},
	}
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "bot", AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
	require.False(t, eventVisibleToCaller(scoped, evt), "a scoped caller must not see skill approval events")

	admin := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	require.True(t, eventVisibleToCaller(admin, evt))
	require.True(t, eventVisibleToCaller(context.Background(), evt))
}
