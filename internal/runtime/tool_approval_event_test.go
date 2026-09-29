package runtime

import (
	"context"
	"path/filepath"
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

func TestRecordToolBlocksForSecurityApproval_EmitsAuditAndReviewEventsWithoutRewritingState(t *testing.T) {
	rt := setupQuarantineRuntime(t, boolP(true), []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "delete_issue", Status: storage.ToolApprovalStatusApproved,
		CurrentHash: "h1", ApprovedHash: "h1", ApprovedBy: "reviewer", Disabled: true,
	}))
	before, err := rt.storageManager.GetToolApproval("github", "delete_issue")
	require.NoError(t, err)

	events := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(events)
	rt.RecordToolBlocksForSecurityApproval("github", []string{"delete_issue"}, "reviewer")

	gotAudit, gotReview, gotServers := false, false, false
	deadline := time.After(2 * time.Second)
	for !gotAudit || !gotReview || !gotServers {
		select {
		case event := <-events:
			switch event.Type {
			case EventTypeActivityToolQuarantineChange:
				gotAudit = true
				assert.Equal(t, "delete_issue", event.Payload["tool_name"])
				assert.Equal(t, "tool_blocked", event.Payload["action"])
			case EventTypeReviewChanged:
				gotReview = true
				assert.Equal(t, "github", event.Payload["server"])
			case EventTypeServersChanged:
				gotServers = true
				assert.Equal(t, "security_approval_tool_blocks", event.Payload["reason"])
				assert.Equal(t, "reviewer", event.Payload["blocked_by"])
			}
		case <-deadline:
			t.Fatalf("missing audit/review/server event: audit=%t review=%t servers=%t", gotAudit, gotReview, gotServers)
		}
	}
	after, err := rt.storageManager.GetToolApproval("github", "delete_issue")
	require.NoError(t, err)
	assert.Equal(t, before, after, "the audit event bridge must not rewrite atomically saved state")
}

func TestLoadConfiguredServersEmitsReviewChangedForQuarantinedServerTransitions(t *testing.T) {
	rt := setupQuarantineRuntime(t, boolP(true), nil)
	events := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(events)

	added := &config.Config{DataDir: rt.Config().DataDir, Listen: rt.Config().Listen, Servers: []*config.ServerConfig{{
		Name: "empty-quarantine", Enabled: false, Quarantined: true,
	}}}
	require.NoError(t, rt.LoadConfiguredServers(added))
	requireReviewChangedForServer(t, events, "empty-quarantine")

	// The queue includes quarantined servers before their tool definitions have
	// been captured. Removing one must wake an already-open review queue after
	// storage deletion completes.
	removed := &config.Config{DataDir: added.DataDir, Listen: added.Listen}
	require.NoError(t, rt.LoadConfiguredServers(removed))
	requireReviewChangedForServer(t, events, "empty-quarantine")
	require.Eventually(t, func() bool {
		_, err := rt.storageManager.GetUpstreamServer("empty-quarantine")
		return err != nil
	}, 2*time.Second, 10*time.Millisecond, "removed server should be deleted from storage")
}

func requireReviewChangedForServer(t *testing.T, events <-chan Event, server string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == EventTypeReviewChanged && event.Payload["server"] == server {
				return
			}
		case <-deadline:
			t.Fatalf("expected review.changed for %q", server)
		}
	}
}

func TestLoadConfiguredServersEmitsReviewChangedWhenTrustedPendingServerIsRemoved(t *testing.T) {
	rt := setupQuarantineRuntime(t, boolP(true), []*config.ServerConfig{{
		Name: "trusted-pending", Enabled: false, Quarantined: false,
	}})
	require.NoError(t, rt.LoadConfiguredServers(rt.Config()))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "trusted-pending", ToolName: "new_tool", Status: storage.ToolApprovalStatusPending,
		CurrentHash: "pending-hash", CurrentDescription: "Needs review",
	}))
	before, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, before.Count)

	events := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(events)
	updated := config.DefaultConfig()
	updated.DataDir = rt.Config().DataDir
	updated.Listen = rt.Config().Listen
	updated.Servers = nil
	_, err = rt.ApplyConfig(updated, filepath.Join(t.TempDir(), "mcp_config.json"))
	require.NoError(t, err)
	require.NoError(t, rt.LoadConfiguredServers(updated))

	requireReviewChangedForServer(t, events, "trusted-pending")
	require.Eventually(t, func() bool {
		queue, queueErr := rt.GetReviewQueue(context.Background())
		return queueErr == nil && queue.Count == 0
	}, 2*time.Second, 10*time.Millisecond, "removed pending-tool server should disappear from review queue")
}
