package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-j J15 (#1437 item 3): an activity.internal_tool_call.completed event
// carries the token prefix since the session does (the funnel has no request
// context). Its owner keeps the attribution, another scoped token never does
// and an admin always does, exactly like tool_call.completed.
func internalAttributionEvent(tokenName, prefix string) internalRuntime.Event {
	evt := attributionEvent(tokenName, prefix)
	evt.Type = internalRuntime.EventTypeActivityInternalToolCall
	evt.Payload["internal_tool_name"] = "retrieve_tools"
	delete(evt.Payload, "server_name")
	delete(evt.Payload, "tool_name")
	return evt
}

func TestSSEInternalAttribution(t *testing.T) {
	srv := NewServer(&scopeParamsController{}, zap.NewNop().Sugar(), nil)
	evt := internalAttributionEvent("ro-bot", "mcp_agt_robo")

	owner := agentCtxFor("ro-bot", "mcp_agt_robo")
	got := renderedAttribution(t, srv, owner, evt)
	require.NotNil(t, got, "the owning scoped subscriber keeps its own internal-call attribution")
	assert.Equal(t, "ro-bot", got["token_name"])

	other := agentCtxFor("other-bot", "mcp_agt_othr")
	assert.Nil(t, renderedAttribution(t, srv, other, evt), "another scoped token never sees it")

	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
	require.NotNil(t, renderedAttribution(t, srv, admin, evt))

	// An event with no prefix (the pre-fix shape) stays foreign to everyone
	// scoped: nothing is widened by the fix.
	legacy := internalAttributionEvent("ro-bot", "")
	legacy.Timestamp = time.Now()
	assert.Nil(t, renderedAttribution(t, srv, owner, legacy))
}
