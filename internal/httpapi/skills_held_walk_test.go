package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
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
		// T031: the held skills are owed a review, so the queue and the
		// attention list carry them, for an administrator.
		c.queue = heldSkillQueue()
		c.attentionItemsOverride = heldSkillAttention()
	})

	// Positive control: the administrator does see the held skill, so the
	// fixture is able to leak.
	admin := skillsDo(t, srv, http.MethodGet, "/api/v1/servers/alpha/skills", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	require.Contains(t, admin.Body.String(), marker)
	for _, p := range []string{"/api/v1/review", "/api/v1/attention"} {
		body := skillsDo(t, srv, http.MethodGet, p, scopeAdminAPIKey).Body.String()
		require.Containsf(t, body, "skill", "the administrator sees held skills on %s", p)
	}
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
			for _, word := range heldSkillWireWords {
				require.NotContainsf(t, rec.Body.String(), word,
					"GET %s showed held-skill review data to a scoped caller (status %d)", p, rec.Code)
			}
		}
	}
	for _, call := range ctrl.recorded() {
		require.NotContainsf(t, []string{"ListSkillRecords", "SkillFileText"}, call,
			"a scoped caller made a route read skill data: %v", ctrl.recorded())
	}
}

// heldSkillWireWords are the field and kind names that only held-skill review
// data uses on the wire (T031). None may reach a scoped caller.
var heldSkillWireWords = []string{"skill_review", "skills_pending", "skills_changed"}

// heldSkillQueue is a review queue in which alpha owes a tool review and has
// held skills, and beta owes only a skill review.
func heldSkillQueue() *internalRuntime.ReviewQueue {
	return &internalRuntime.ReviewQueue{Count: 2, Servers: []internalRuntime.ReviewQueueRow{
		{Server: "alpha", Kind: "tool_review", Enabled: true, Pending: 1, SkillsPending: 2, SkillsChanged: 1},
		{Server: "beta", Kind: internalRuntime.ReviewKindSkillReview, Enabled: true, SkillsPending: 3},
	}}
}

func heldSkillAttention() []contracts.AttentionItem {
	srv := func(name string) contracts.AttentionSubject {
		return contracts.AttentionSubject{Type: "server", ID: name, Name: name}
	}
	return []contracts.AttentionItem{
		{ID: "tool_review:server:alpha:pending", Kind: internalRuntime.AttentionKindToolReview, Rank: internalRuntime.AttentionRankToolReviewPending, Subject: srv("alpha"), Summary: "alpha: 1 new tool(s), needs review"},
		{ID: "skill_review:server:alpha:changed", Kind: internalRuntime.AttentionKindSkillReview, Rank: internalRuntime.AttentionRankSkillReviewChanged, Subject: srv("alpha"), Summary: "alpha: 1 skill(s) changed, needs review"},
		{ID: "skill_review:server:beta:pending", Kind: internalRuntime.AttentionKindSkillReview, Rank: internalRuntime.AttentionRankSkillReviewPending, Subject: srv("beta"), Summary: "beta: 3 new skill(s), needs review"},
	}
}

// heldSkillsReviewServer serves heldSkillQueue and heldSkillAttention to an
// agent token whose reach includes both servers.
func heldSkillsReviewServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := scopeFixtureConfig(false)
	cfg.EnableSkills = true
	ctrl := &skillsTestController{
		scopeController: scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true,
			attentionItemsOverride: heldSkillAttention()},
		recs: map[string][]*storage.SkillApprovalRecord{}, status: map[string]internalRuntime.SkillsServerStatus{},
		files: map[string][]byte{}, queue: heldSkillQueue(),
	}
	return scopedAgentServer(t, ctrl, []string{"alpha", "beta"})
}

func TestReviewQueue_HeldSkillsAreForAdministratorsOnly(t *testing.T) {
	srv, token := heldSkillsReviewServer(t)

	admin := scopeDecodeData(t, skillsDo(t, srv, http.MethodGet, "/api/v1/review", scopeAdminAPIKey))
	assert.EqualValues(t, 2, admin["count"])
	rows := admin["servers"].([]interface{})
	require.Len(t, rows, 2)
	alpha := rows[0].(map[string]interface{})
	assert.EqualValues(t, 2, alpha["skills_pending"])
	assert.EqualValues(t, 1, alpha["skills_changed"])
	assert.Equal(t, "skill_review", rows[1].(map[string]interface{})["kind"])

	body := scopeGet(t, srv, "/api/v1/review", token)
	scoped := scopeDecodeData(t, body)
	assert.EqualValues(t, 1, scoped["count"], "a skill-only row is not counted for a scoped caller")
	rows = scoped["servers"].([]interface{})
	require.Len(t, rows, 1)
	row := rows[0].(map[string]interface{})
	assert.Equal(t, "alpha", row["server"])
	assert.Equal(t, "tool_review", row["kind"])
	assert.EqualValues(t, 1, row["pending"], "the tool counts stay")
	for _, word := range heldSkillWireWords {
		assert.NotContains(t, body.Body.String(), word)
	}
}

func TestAttention_SkillReviewIsForAdministratorsOnly(t *testing.T) {
	srv, token := heldSkillsReviewServer(t)

	admin := scopeDecodeData(t, skillsDo(t, srv, http.MethodGet, "/api/v1/attention", scopeAdminAPIKey))
	assert.EqualValues(t, 3, admin["count"])

	body := scopeGet(t, srv, "/api/v1/attention", token)
	scoped := scopeDecodeData(t, body)
	assert.EqualValues(t, 1, scoped["count"])
	items := scoped["items"].([]interface{})
	require.Len(t, items, 1)
	assert.Equal(t, "tool_review:server:alpha:pending", items[0].(map[string]interface{})["id"])
	assert.NotContains(t, body.Body.String(), "skill")
}

func TestSkillVisibleTo(t *testing.T) {
	assert.True(t, skillVisibleTo(context.Background()), "no auth context: the local administrator")
	assert.True(t, skillVisibleTo(auth.WithAuthContext(context.Background(), auth.AdminContext())))
	for name, ac := range map[string]*auth.AuthContext{
		"agent":        {Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}},
		"pinned agent": {Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}, ProfilePin: "p"},
		"tenant user":  {Type: auth.AuthTypeUser, AllowedServers: []string{"alpha"}},
	} {
		assert.Falsef(t, skillVisibleTo(auth.WithAuthContext(context.Background(), ac)), "%s", name)
	}
}

func TestSSE_AttentionChangedDropsSkillReviewForScopedCallers(t *testing.T) {
	payload := map[string]interface{}{"count": 2, "items": []internalRuntime.AttentionEventItem{
		{ID: "tool_review:server:alpha:pending", Kind: internalRuntime.AttentionKindToolReview, SubjectType: "server", SubjectID: "alpha"},
		{ID: "skill_review:server:alpha:pending", Kind: internalRuntime.AttentionKindSkillReview, SubjectType: "server", SubjectID: "alpha"},
	}}
	admin := renderAttentionChangedForCaller(auth.WithAuthContext(context.Background(), auth.AdminContext()), payload)
	assert.Equal(t, 2, admin["count"])
	scoped := renderAttentionChangedForCaller(auth.WithAuthContext(context.Background(),
		&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}}), payload)
	assert.Equal(t, []string{"tool_review:server:alpha:pending"}, scoped["ids"])
	assert.Equal(t, 1, scoped["count"])
}

func TestSSE_SkillsReviewChangedIsForAdministratorsOnly(t *testing.T) {
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
	skills := internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged,
		Payload: map[string]any{"server": "alpha", internalRuntime.ReviewChangedSkillsKey: true}}
	tools := internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged, Payload: map[string]any{"server": "alpha"}}
	assert.False(t, eventVisibleToCaller(scoped, skills), "a skills review change is administrator-only")
	assert.True(t, eventVisibleToCaller(admin, skills))
	assert.True(t, eventVisibleToCaller(scoped, tools), "a tool review change on a visible server still reaches it")
}

// End to end over the SSE stream: the administrator's attention.changed frame
// carries the skill_review id, the scoped subscriber's does not.
func TestSSE_AttentionChangedSkillReviewNeverReachesAScopedSubscriber(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	evt := internalRuntime.Event{Type: internalRuntime.EventTypeAttentionChanged, Timestamp: time.Now(),
		Payload: map[string]any{"count": 2, "items": []internalRuntime.AttentionEventItem{
			{ID: "skill_review:server:alpha:pending", Kind: internalRuntime.AttentionKindSkillReview, SubjectType: "server", SubjectID: "alpha"},
			{ID: "sign_in_required:server:alpha", Kind: internalRuntime.AttentionKindSignInRequired, SubjectType: "server", SubjectID: "alpha"},
		}}}
	deadline := time.Now().Add(10 * time.Second)
	adminDone := make(chan sseEvent, 1)
	go func() { adminDone <- readSSEUntil(t, adminBody, "attention.changed", deadline) }()
	agentDone := make(chan sseEvent, 1)
	go func() { agentDone <- readSSEUntil(t, agentBody, "attention.changed", deadline) }()
	ctrl.publishToAll(evt)

	adminIDs, _ := (<-adminDone).Data["payload"].(map[string]interface{})["ids"].([]interface{})
	assert.Len(t, adminIDs, 2)
	agentIDs, _ := (<-agentDone).Data["payload"].(map[string]interface{})["ids"].([]interface{})
	assert.Equal(t, []interface{}{"sign_in_required:server:alpha"}, agentIDs)
}
