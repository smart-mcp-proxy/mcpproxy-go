package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestApproveTools_EmitsServersChanged verifies that ApproveTools publishes a
// servers.changed runtime event after successfully updating tool approval
// records. Without this, a Servers/overview page open in another browser tab
// has no way to know it should re-fetch — see issue #438. Subscribed via the
// same mechanism the SSE handler uses, so this test exercises the end-to-end
// publish path.
func TestApproveTools_EmitsServersChanged(t *testing.T) {
	rt := setupQuarantineRuntime(t, boolP(true), []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: false},
	})

	// Pre-seed an approval record in the "pending" state so ApproveTools has
	// real work to do.
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		Status:             storage.ToolApprovalStatusPending,
		CurrentHash:        "h1",
		CurrentDescription: "Creates a GitHub issue",
		CurrentSchema:      `{"type":"object"}`,
	}))

	events := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(events)

	require.NoError(t, rt.ApproveTools("github", []string{"create_issue"}, "test-user"))

	// We expect at least one EventTypeServersChanged with reason=tools_approved
	// to land within a reasonable window. The event-bus uses a non-blocking
	// publish, so a generous timeout shouldn't slow CI in practice.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Type != EventTypeServersChanged {
				continue
			}
			assert.Equal(t, "tools_approved", evt.Payload["reason"])
			assert.Equal(t, "github", evt.Payload["server"])
			assert.Equal(t, 1, evt.Payload["approved_count"])
			assert.Equal(t, "test-user", evt.Payload["approved_by"])
			return
		case <-deadline:
			t.Fatalf("expected servers.changed (tools_approved) event, none received within 2s")
		}
	}
}

// TestApproveTools_NoEventOnNoOp verifies that when ApproveTools is called for
// a tool that has no approval record (already approved / never seen), it does
// NOT emit a stray servers.changed event. The bus is shared with other
// subscribers so we shouldn't publish when nothing changed.
func TestApproveTools_NoEventOnNoOp(t *testing.T) {
	rt := setupQuarantineRuntime(t, boolP(true), []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: false},
	})

	events := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(events)

	// No pre-seeded record — ApproveTools should log a warning and continue
	// without saving anything, which means approved=0 and no event.
	require.NoError(t, rt.ApproveTools("github", []string{"nonexistent"}, "test-user"))

	select {
	case evt := <-events:
		if evt.Type == EventTypeServersChanged {
			t.Fatalf("did not expect servers.changed when no approvals were applied; got %+v", evt)
		}
	case <-time.After(150 * time.Millisecond):
		// expected: no event delivered
	}
}
