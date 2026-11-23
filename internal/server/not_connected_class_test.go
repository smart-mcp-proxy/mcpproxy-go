package server

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Release QA F1: a call to a server that waits for a login is recorded as an
// authentication failure, not as an unreachable server.
func TestNotConnectedCall_LoggedOutServerIsAnAuthFailure(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("f1-logout", "")

	client, ok := f.proxy.upstreamManager.GetClient("github")
	require.True(t, ok)
	// What `mcpproxy auth logout` leaves behind: the logged-out flag, then a
	// disconnect (Runtime.TriggerOAuthLogout).
	client.SetUserLoggedOut(true)
	require.NoError(t, client.Disconnect())
	require.False(t, client.IsConnected())

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Contains(t, rec.Body.String(), "is not connected", "the call reached the not-connected path")

	row := f.eventuallyFailedCall(t, "f1-logout")
	assert.Equal(t, "auth", row.ErrorClass, "the server is up and needs a login")
}

// A server parked awaiting a login (pending auth) is an authentication
// failure too.
func TestNotConnectedCall_PendingAuthIsAnAuthFailure(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("f1-pending", "")

	client, ok := f.proxy.upstreamManager.GetClient("github")
	require.True(t, ok)
	client.SetUserLoggedOut(true) // keeps the reconnect loop away
	require.NoError(t, client.Disconnect())
	client.SetUserLoggedOut(false)
	client.StateManager.SetPendingAuth(errors.New("OAuth authorization required"))

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Contains(t, rec.Body.String(), "is not connected", "the call reached the not-connected path")

	row := f.eventuallyFailedCall(t, "f1-pending")
	assert.Equal(t, "auth", row.ErrorClass)
}

func (f *restV3Fixture) eventuallyFailedCall(t *testing.T, token string) *storage.ActivityRecord {
	t.Helper()
	var got *storage.ActivityRecord
	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.TokenName == token && r.Type == storage.ActivityTypeToolCall && r.Status == storage.ActivityStatusError {
				got = r
				return true
			}
		}
		return false
	}, attributionWait, 10*time.Millisecond, "no failed tool_call row for %s", token)
	return got
}

// Release QA F1, fifth caller: a code_execution sub-call to a server that is
// not connected is recorded by its cause, not as a proxy failure.
func TestNotConnectedCall_CodeExecutionSubCall(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("f1-js", "")

	client, ok := f.proxy.upstreamManager.GetClient("github")
	require.True(t, ok)
	client.SetUserLoggedOut(true)
	require.NoError(t, client.Disconnect())

	f.callTool(key, "code_execution", map[string]interface{}{
		"code": `try { call_tool("github", "list_issues", {}) } catch (e) {} ; 1`,
	}, "")

	var sub *storage.ActivityRecord
	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.TokenName == "f1-js" && r.Type == storage.ActivityTypeToolCall && r.ServerName == "github" && r.Status == storage.ActivityStatusError {
				sub = r
				return true
			}
		}
		return false
	}, attributionWait, 10*time.Millisecond, "no failed sub-call row")
	assert.Equal(t, "auth", sub.ErrorClass, "a logged-out server's sub-call needs a login")
}
