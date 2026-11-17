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

func TestSkillAccessActivity_ScopedTokenSeesOnlyRowsOfItsServers(t *testing.T) {
	ctrl := &scopeParamsController{records: skillAccessRecords()}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	assert.Equal(t, []string{"sa-alpha"}, activityIDs(t, srv, "/api/v1/activity?type=skill_access", token),
		"the list shows only the caller's own server's row")

	for _, id := range []string{"sa-beta", "sa-probe", "sa-search"} {
		rec := scopeGet(t, srv, "/api/v1/activity/"+id, token)
		assert.Equal(t, http.StatusNotFound, rec.Code, id)
		assert.NotContains(t, rec.Body.String(), "probed", id)
	}
	assert.Equal(t, http.StatusOK, scopeGet(t, srv, "/api/v1/activity/sa-alpha", token).Code)

	export := scopeGet(t, srv, "/api/v1/activity/export?type=skill_access&format=json", token)
	require.Equal(t, http.StatusOK, export.Code)
	body := export.Body.String()
	assert.Contains(t, body, "sa-alpha")
	for _, leak := range []string{"sa-beta", "sa-probe", "sa-search", "secret plans", "beta:probed"} {
		assert.False(t, strings.Contains(body, leak), "export leaks %q", leak)
	}
}

func TestSkillAccessActivity_AdministratorSeesEveryRow(t *testing.T) {
	ctrl := &scopeParamsController{records: skillAccessRecords()}
	srv := scopeParamsAdminServer(t, ctrl)
	assert.ElementsMatch(t, []string{"sa-alpha", "sa-beta", "sa-probe", "sa-search"},
		activityIDs(t, srv, "/api/v1/activity?type=skill_access", scopeAdminAPIKey))
}
