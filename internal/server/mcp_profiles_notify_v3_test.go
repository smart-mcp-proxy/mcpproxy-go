//go:build !server

package server

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 108-h H17: no new notification code; the binding change goes through the
// clients service, which already notifies the credential's live sessions.

// TestProfilesTool_AssignSendsListChangedLikeREST: an MCP assign reaches the
// binding's live session as tools/list_changed, exactly like the REST route.
func TestProfilesTool_AssignSendsListChangedLikeREST(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	srv, api := newNotifyServer(t)
	proxy := srv.mcpProxy
	proxy.SetAdminViews(api)
	mintClient(t, srv, "cursor", "ro")
	cursor := clientCtx("cursor", "ro", auth.ProfileModeLocked)
	session := initSession(t, defaultSurface(proxy), "cursor-assign", cursor)
	session.drainListChanged()

	req := mcpCallRequest(map[string]any{"operation": "assign", "client": "cursor", "profile": "full"})
	done := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, _ := proxy.handleProfiles(apiKeyCtx(), req)
		done <- res
	}()
	select {
	case res := <-done:
		require.False(t, res.IsError, resultText(t, res))
	case <-time.After(5 * time.Second):
		t.Fatal("assign did not return: a lock is held while the sessions are notified")
	}
	require.Eventually(t, func() bool { return session.drainListChanged() > 0 }, 3*time.Second, 5*time.Millisecond,
		"the bound session is sent tools/list_changed")

	// REST sends the same notification for the same kind of change.
	w := putBinding(t, api, "cursor", `{"profile":"ro"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Eventually(t, func() bool { return session.drainListChanged() > 0 }, 3*time.Second, 5*time.Millisecond)
}
