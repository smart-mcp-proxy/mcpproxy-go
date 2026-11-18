package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 FR-024: skill_access rows follow the ordinary server-reach rule of
// every activity read. A load names its origin server; a search, and an id the
// caller could not reach, name none and so are administrator-only.

func skillAccessRecord(id, server, action, outcome, skillID string) *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID: id, Type: storage.ActivityTypeSkillAccess, ServerName: server,
		ToolName: "load_skill", Status: storage.ActivityStatusSuccess,
		Timestamp: time.Now().UTC().Add(-time.Minute),
		Arguments: map[string]interface{}{"id": skillID},
		Metadata:  map[string]interface{}{"action": action, "outcome": outcome, "skill_id": skillID},
	}
}

func skillAccessRecords() []*storage.ActivityRecord {
	search := skillAccessRecord("sa-search", "", "search", "success", "")
	search.Arguments = map[string]interface{}{"query": "secret plans of the beta team"}
	return []*storage.ActivityRecord{
		skillAccessRecord("sa-alpha", "alpha", "load", "success", "alpha:helper"),
		skillAccessRecord("sa-beta", "beta", "load", "success", "beta:helper"),
		// What a not-found load of an unreachable id looks like: no server.
		skillAccessRecord("sa-probe", "", "load", "not_found", "beta:probed"),
		search,
	}
}

// A skill access row names the id its caller asked for, which may be a skill
// that is held, on a server the reader can see. No scoped reader gets any of
// them, on any activity door (FR-019a).
func TestSkillAccessActivity_ScopedTokenSeesNoRow(t *testing.T) {
	ctrl := &scopeParamsController{records: skillAccessRecords()}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	assert.Empty(t, activityIDs(t, srv, "/api/v1/activity?type=skill_access", token))
	assert.Empty(t, activityIDs(t, srv, "/api/v1/activity", token))

	for _, id := range []string{"sa-alpha", "sa-beta", "sa-probe", "sa-search"} {
		rec := scopeGet(t, srv, "/api/v1/activity/"+id, token)
		assert.Equal(t, http.StatusNotFound, rec.Code, id)
		assert.NotContains(t, rec.Body.String(), "probed", id)
	}

	export := scopeGet(t, srv, "/api/v1/activity/export?type=skill_access&format=json", token)
	require.Equal(t, http.StatusOK, export.Code)
	body := export.Body.String()
	for _, leak := range []string{"sa-alpha", "sa-beta", "sa-probe", "sa-search", "secret plans", "beta:probed"} {
		assert.False(t, strings.Contains(body, leak), "export leaks %q", leak)
	}
}

func TestSkillAccessActivity_AdministratorSeesEveryRow(t *testing.T) {
	ctrl := &scopeParamsController{records: skillAccessRecords()}
	srv := scopeParamsAdminServer(t, ctrl)
	assert.ElementsMatch(t, []string{"sa-alpha", "sa-beta", "sa-probe", "sa-search"},
		activityIDs(t, srv, "/api/v1/activity?type=skill_access", scopeAdminAPIKey))
}
