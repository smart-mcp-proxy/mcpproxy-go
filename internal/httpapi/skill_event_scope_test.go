package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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

// A skill approval row names a skill that may be unapproved, so the activity
// list, its total and the detail route treat it as administrator-only
// (Spec 125 FR-019a), even on a server the caller may see.
func TestActivity_SkillQuarantineRowsAreAdminOnly(t *testing.T) {
	skillRow := &storage.ActivityRecord{
		ID: "activity-skill", Type: storage.ActivityTypeSkillQuarantineChange, ServerName: "everything",
		Status: "held_changed", Timestamp: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]interface{}{"skill": "pdf", "action": "held_changed"},
	}
	toolRow := agentActivityRecord()
	ctrl := &mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{skillRow, toolRow}}

	// An administrator sees both.
	admin := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	data := getJSON(t, admin, "/api/v1/activity")
	require.Len(t, data["activities"].([]interface{}), 2)
	require.EqualValues(t, 2, data["total"])
	getJSON(t, admin, "/api/v1/activity/activity-skill")

	// A scoped caller entitled to the server sees only the tool row, and the
	// total does not count the hidden one.
	srv, token := scopedAgentServer(t, ctrl, []string{"everything"})
	data = scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity", token))
	rows := data["activities"].([]interface{})
	require.Len(t, rows, 1)
	assert.Equal(t, "activity-agent", rows[0].(map[string]interface{})["id"])
	assert.EqualValues(t, 1, data["total"], "the hidden row is not counted")
	data = scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity?type=skill_quarantine_change", token))
	assert.Empty(t, data["activities"])
	assert.EqualValues(t, 0, data["total"])

	// The detail route answers as for an absent id.
	hidden := scopeGet(t, srv, "/api/v1/activity/activity-skill", token)
	absent := scopeGet(t, srv, "/api/v1/activity/activity-nope", token)
	assert.Equal(t, http.StatusNotFound, hidden.Code)
	assert.Equal(t, absent.Code, hidden.Code)
}
