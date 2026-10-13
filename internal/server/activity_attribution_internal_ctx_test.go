package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// REST /tools/call has no MCP session, so the internal_tool_call record must
// take its attribution from the request context like its tool_call twin.
func TestActivityAttribution_RESTInternalToolCallCarriesCallerAttribution(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("ci-bot", "work-readonly")

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var twin, internal *storage.ActivityRecord
	require.Eventually(t, func() bool {
		twin, internal = nil, nil
		for _, r := range f.activities() {
			switch {
			case r.Type == storage.ActivityTypeToolCall && r.ServerName == "github" && r.ToolName == "list_issues" && r.TokenName == "ci-bot":
				twin = r
			case r.Type == storage.ActivityTypeInternalToolCall && r.TokenName == "ci-bot":
				internal = r
			}
		}
		return twin != nil && internal != nil
	}, 5*time.Second, 10*time.Millisecond, "both the tool_call and its internal_tool_call carry the caller's token")

	assert.Equal(t, twin.Profile, internal.Profile)
	assert.Equal(t, twin.ProfileSource, internal.ProfileSource)
	assert.Equal(t, twin.ClientID, internal.ClientID)
	assert.Equal(t, twin.TokenName, internal.TokenName)
	assert.Equal(t, "work-readonly", internal.Profile)
	assert.Equal(t, auth.TokenPrefix(key), internal.TokenPrefix, "the ownership proof is stored on the record")
}

// The activity.internal_tool_call event carries the caller's token prefix in
// its attribution, so a scoped SSE subscriber recognises its own event.
func TestActivityAttribution_InternalToolCallEventCarriesTokenPrefix(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("ci-bot", "work-readonly")
	events := f.rt.SubscribeEvents()
	defer f.rt.UnsubscribeEvents(events)

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Type != runtime.EventTypeActivityInternalToolCall {
				continue
			}
			attr, _ := evt.Payload["attribution"].(map[string]any)
			require.NotNil(t, attr, "the event carries an attribution")
			assert.Equal(t, auth.TokenPrefix(key), attr["_token_prefix"])
			assert.Equal(t, "ci-bot", attr["token_name"])
			return
		case <-deadline:
			t.Fatal("no activity.internal_tool_call event")
		}
	}
}
