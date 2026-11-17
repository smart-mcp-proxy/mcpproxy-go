package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 FR-019a: everything about a skill that is not approved is for an
// administrator only. Being in scope for the server is not enough.
//
// The walk drives every GET route under /api/v1 with an agent token whose
// reach includes the server, while that server has a held skill whose name,
// path, description, hash, file name and scanner evidence all carry one
// marker. The marker must appear in no answer, and no route may even ask the
// controller for skill records or skill text on behalf of that caller. A new
// read door that shows held skills to a scoped caller fails here.
func TestHeldSkillAppearsOnNoReadDoorForAScopedCaller(t *testing.T) {
	const marker = "zzheldskillmarker"
	ctrl, srv, token := skillsServer(t, true, func(c *skillsTestController) {
		rec := skillRec(marker, storage.SkillStatusPending, "sk1-"+marker,
			storage.SkillFile{RelPath: "SKILL.md", Size: 10, Digest: "sha256:" + marker, Text: true, Stored: true},
			storage.SkillFile{RelPath: marker + ".md", Size: 10, Digest: "sha256:abc", Text: true, Stored: true})
		rec.Description = "held " + marker
		rec.Scan = &storage.SkillScan{Verdict: "dangerous", Hash: rec.CurrentHash,
			Findings: []storage.SkillScanFinding{{File: "SKILL.md", Rule: "r", Severity: "high", Evidence: marker}}}
		changed := skillRec(marker+"-changed", storage.SkillStatusChanged, "sk1-new-"+marker,
			storage.SkillFile{RelPath: "SKILL.md", Size: 10, Digest: "sha256:def", Text: true, Stored: true})
		cur := changed.Current
		changed.Approved = &cur
		changed.ApprovedHash = "sk1-old-" + marker
		c.recs["alpha"] = []*storage.SkillApprovalRecord{rec, changed}
		c.files[marker+"|SKILL.md|current"] = []byte("# " + marker)
	})

	// Positive control: the administrator does see the held skill, so the
	// fixture is able to leak.
	admin := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	require.Contains(t, admin.Body.String(), marker)
	ctrl.calls = nil

	var routes []string
	require.NoError(t, chi.Walk(srv.Router(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && strings.HasPrefix(route, "/api/v1") {
			routes = append(routes, route)
		}
		return nil
	}))
	require.GreaterOrEqual(t, len(routes), minGetRoutes, "the walk matched too few routes to mean anything")

	for _, route := range routes {
		path := fillRouteParams(route)
		for _, p := range []string{path, path + "?skill=" + marker, path + "?skill=" + marker + "&copy=approved"} {
			rec := scopeGet(t, srv, p, token)
			require.NotContainsf(t, rec.Body.String(), marker,
				"GET %s showed a held skill to a scoped caller (status %d)", p, rec.Code)
		}
	}
	for _, call := range ctrl.recorded() {
		require.NotContainsf(t, []string{"ListSkillRecords", "SkillFileText"}, call,
			"a scoped caller made a route read skill data: %v", ctrl.recorded())
	}
}
