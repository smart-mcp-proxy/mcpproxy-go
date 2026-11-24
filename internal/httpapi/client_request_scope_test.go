package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A client request row is about another caller's client (#1964): a token
// scoped to the same server reads it on no activity door.
func TestClientRequestActivity_IsAdminOnly(t *testing.T) {
	ctrl := &scopeParamsController{records: []*storage.ActivityRecord{{
		ID: "cr-alpha", Type: storage.ActivityTypeClientRequest, ServerName: "alpha",
		ToolName: "elicitation/create", Status: storage.ActivityStatusSuccess,
		Timestamp: time.Now().UTC().Add(-time.Minute),
		Arguments: map[string]interface{}{"message": "deploy the secret plan?"},
		Metadata:  map[string]interface{}{"outcome": "forwarded", "action": "accept"},
	}}}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	assert.Empty(t, activityIDs(t, srv, "/api/v1/activity?type=client_request", token))
	assert.Empty(t, activityIDs(t, srv, "/api/v1/activity", token))
	assert.Equal(t, http.StatusNotFound, scopeGet(t, srv, "/api/v1/activity/cr-alpha", token).Code)
	export := scopeGet(t, srv, "/api/v1/activity/export?type=client_request&format=json", token)
	require.Equal(t, http.StatusOK, export.Code)
	assert.NotContains(t, export.Body.String(), "secret plan")

	admin := scopeParamsAdminServer(t, ctrl)
	assert.Equal(t, []string{"cr-alpha"}, activityIDs(t, admin, "/api/v1/activity?type=client_request", scopeAdminAPIKey))
}

func TestSSE_ClientRequestIsAdminOnly(t *testing.T) {
	evt := internalRuntime.Event{
		Type:    internalRuntime.EventTypeActivityClientRequest,
		Payload: map[string]any{"server_name": "alpha", "method": "roots/list", "outcome": "forwarded"},
	}
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "bot", AllowedServers: []string{"alpha"}, Permissions: []string{auth.PermRead},
	})
	require.False(t, eventVisibleToCaller(scoped, evt))
	admin := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	require.True(t, eventVisibleToCaller(admin, evt))
}
