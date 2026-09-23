package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The Web UI's Auth Type / Agent filters (Spec 028) read the caller identity
// off each row, but the identity lives in the internal `_auth_*` argument keys
// that the REST boundary strips. Before auth_type/agent_name were surfaced as
// typed fields, the Agent dropdown was always empty and Auth Type = Agent
// filtered out every row.

func agentActivityRecord() *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID:         "activity-agent",
		Type:       storage.ActivityTypeToolCall,
		ServerName: "everything",
		ToolName:   "echo",
		Status:     "success",
		Timestamp:  time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		Arguments: map[string]interface{}{
			"message":            "hi",
			"_auth_auth_type":    "agent",
			"_auth_agent_name":   "ci-bot",
			"_auth_token_prefix": "mcp_agt_abcd",
			"_auth_user_id":      "u-1",
			"_auth_user_email":   "u@example.com",
		},
	}
}

func TestActivityList_ExposesAuthIdentity(t *testing.T) {
	for _, target := range []string{"/api/v1/activity", "/api/v1/activity?exclude_payloads=true"} {
		t.Run(target, func(t *testing.T) {
			srv := NewServer(&mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{agentActivityRecord()}}, zap.NewNop().Sugar(), nil)

			data := getJSON(t, srv, target)
			activities := data["activities"].([]interface{})
			require.Len(t, activities, 1)
			activity := activities[0].(map[string]interface{})

			assert.Equal(t, "agent", activity["auth_type"])
			assert.Equal(t, "ci-bot", activity["agent_name"])

			// Only the two filterable identity fields cross the boundary; the
			// token prefix and server-edition user identity stay internal.
			raw, err := json.Marshal(activity)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "_auth_")
			assert.NotContains(t, string(raw), "mcp_agt_abcd")
			assert.NotContains(t, string(raw), "u@example.com")
		})
	}
}

func TestActivityDetail_ExposesAuthIdentity(t *testing.T) {
	srv := NewServer(&mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{agentActivityRecord()}}, zap.NewNop().Sugar(), nil)

	data := getJSON(t, srv, "/api/v1/activity/activity-agent")
	activity := data["activity"].(map[string]interface{})

	assert.Equal(t, "agent", activity["auth_type"])
	assert.Equal(t, "ci-bot", activity["agent_name"])
}

func TestActivityList_OmitsAuthIdentityWhenAbsent(t *testing.T) {
	record := agentActivityRecord()
	record.Arguments = map[string]interface{}{"message": "hi"}
	srv := NewServer(&mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{record}}, zap.NewNop().Sugar(), nil)

	data := getJSON(t, srv, "/api/v1/activity")
	activity := data["activities"].([]interface{})[0].(map[string]interface{})

	assert.NotContains(t, activity, "auth_type")
	assert.NotContains(t, activity, "agent_name")
}

// A scoped caller (agent token) is entitled to rows by SERVER, not by caller.
// Lifting the identity must not hand it the names of every other agent token
// that touched a shared server — that inventory is admin-only (GET /tokens).
// Its own rows keep their identity; everyone else's are blanked.
func TestActivity_ScopedCallerSeesOnlyOwnIdentity(t *testing.T) {
	own := agentActivityRecord()
	own.ID = "activity-own"
	own.Arguments["_auth_agent_name"] = "scoped-ci"
	other := agentActivityRecord()
	other.ID = "activity-other"
	other.Arguments["_auth_agent_name"] = "finance-bot"
	admin := agentActivityRecord()
	admin.ID = "activity-admin"
	admin.Arguments = map[string]interface{}{"_auth_auth_type": "admin"}

	ctrl := &mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{own, other, admin}}
	srv, token := scopedAgentServer(t, ctrl, []string{"*"})

	identity := func(a map[string]interface{}) [2]interface{} { return [2]interface{}{a["auth_type"], a["agent_name"]} }

	data := scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity", token))
	got := map[string][2]interface{}{}
	for _, row := range data["activities"].([]interface{}) {
		a := row.(map[string]interface{})
		got[a["id"].(string)] = identity(a)
	}
	assert.Equal(t, [2]interface{}{"agent", "scoped-ci"}, got["activity-own"])
	assert.Equal(t, [2]interface{}{nil, nil}, got["activity-other"], "another agent's name leaked to a scoped caller")
	assert.Equal(t, [2]interface{}{nil, nil}, got["activity-admin"])

	detail := scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity/activity-other", token))
	assert.Equal(t, [2]interface{}{nil, nil}, identity(detail["activity"].(map[string]interface{})))
	detail = scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity/activity-own", token))
	assert.Equal(t, [2]interface{}{"agent", "scoped-ci"}, identity(detail["activity"].(map[string]interface{})))
}
