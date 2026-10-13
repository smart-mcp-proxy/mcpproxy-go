package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// policy_decision and prompt_get rows carry no _auth_token_prefix argument, so
// the persisted TokenPrefix is the only ownership proof a scoped caller has.
func TestActivityService_PolicyDecisionAndPromptGetPersistTokenPrefix(t *testing.T) {
	attr := ActivityAttribution{TokenName: "client-cursor", TokenPrefix: "mcp_cli_abcd", Profile: "ro", ProfileSource: "binding", ClientID: "cursor"}

	t.Run("policy_decision", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		svc.handleEvent(Event{
			Type:      EventTypeActivityPolicyDecision,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name": "github", "tool_name": "create_issue", "session_id": "s1", "request_id": "r1",
				"decision": "blocked", "reason": "above tier cap", "block_reason": "profile_tier",
				"attribution": attr.payload(),
			},
		})
		rec := onlyRecord(t, store)
		assert.Equal(t, storage.ActivityTypePolicyDecision, rec.Type)
		assert.Equal(t, "mcp_cli_abcd", rec.TokenPrefix)
		assert.True(t, (&storage.ActivityIdentityOwner{TokenName: "client-cursor", TokenPrefix: "mcp_cli_abcd"}).Owns(rec))
	})

	t.Run("prompt_get", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		svc.handleEvent(Event{
			Type:      EventTypeActivityPromptGet,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name": "github", "prompt_name": "review", "session_id": "s1", "request_id": "r2",
				"status": "success", "attribution": attr.payload(),
			},
		})
		rec := onlyRecord(t, store)
		require.Equal(t, storage.ActivityTypePromptGet, rec.Type)
		assert.Equal(t, "mcp_cli_abcd", rec.TokenPrefix)
		assert.True(t, (&storage.ActivityIdentityOwner{TokenName: "client-cursor", TokenPrefix: "mcp_cli_abcd"}).Owns(rec))
	})
}
