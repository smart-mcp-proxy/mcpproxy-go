package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-j J15 (#1437 item 3): the internal-tool completion funnel has no
// request context, so its attribution used to be built from the session alone.
// The session knew the token NAME but not its display prefix, and the SSE
// ownership check needs both: a scoped subscriber's own
// activity.internal_tool_call.completed event was treated as foreign and lost
// its attribution. The session now carries the prefix too.
func TestInternalToolCall_AttributionCarriesTokenPrefix(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	registerAttributionSession(f, "s-int", "Claude Code", "ro-bot", "")
	f.proxy.sessionStore.SetSessionTokenPrefix("s-int", "mcp_agt_robo")

	ch := f.rt.SubscribeEvents()
	defer f.rt.UnsubscribeEvents(ch)

	f.proxy.emitActivityInternalToolCall("retrieve_tools", "", "", "", "s-int", "req-int-1", "success", "", 5, nil, "ok", nil, "")

	var attr map[string]any
	require.Eventually(t, func() bool {
		for {
			select {
			case evt := <-ch:
				if evt.Type != runtime.EventTypeActivityInternalToolCall {
					continue
				}
				attr, _ = evt.Payload["attribution"].(map[string]any)
				return true
			default:
				return false
			}
		}
	}, 5*time.Second, 10*time.Millisecond, "internal tool call event")

	require.NotNil(t, attr, "the event carries an attribution")
	assert.Equal(t, "ro-bot", attr["token_name"])
	assert.Equal(t, "mcp_agt_robo", attr["_token_prefix"], "the token prefix is what lets the owner's SSE stream keep the attribution")
}

// The funnel's attribution for a session with no credential is unchanged: no
// prefix, so nothing is claimed on behalf of a token that was never presented.
func TestInternalToolCall_NoCredentialCarriesNoTokenPrefix(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	registerAttributionSession(f, "s-anon-int", "curl", "", "")

	attr := f.proxy.activityAttribution(context.Background(), "s-anon-int")
	assert.Empty(t, attr.TokenName)
	assert.Empty(t, attr.TokenPrefix)
}
