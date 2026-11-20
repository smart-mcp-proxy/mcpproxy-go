package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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

// skillAwareQueueController records which queue a request asked for.
type skillAwareQueueController struct {
	*skillsTestController
	mu    sync.Mutex
	asked []bool
	plain int
}

func (c *skillAwareQueueController) GetReviewQueue(ctx context.Context) (*internalRuntime.ReviewQueue, error) {
	c.mu.Lock()
	c.plain++
	c.mu.Unlock()
	return c.skillsTestController.GetReviewQueue(ctx)
}

func (c *skillAwareQueueController) GetReviewQueueWithSkills(ctx context.Context, includeSkills bool) (*internalRuntime.ReviewQueue, error) {
	c.mu.Lock()
	c.asked = append(c.asked, includeSkills)
	c.mu.Unlock()
	return c.skillsTestController.GetReviewQueue(ctx)
}

// Review round 2, finding 3: the handler asks the composer for held skills
// only for a caller that may see them, so a scoped request never reads skill
// approvals (its cost cannot depend on how many skills are held).
func TestReviewQueue_ScopedCallerAsksForNoSkills(t *testing.T) {
	cfg := scopeFixtureConfig(false)
	cfg.EnableSkills = true
	inner := &skillsTestController{
		scopeController: scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true},
		recs:            map[string][]*storage.SkillApprovalRecord{}, status: map[string]internalRuntime.SkillsServerStatus{},
		files: map[string][]byte{}, queue: heldSkillQueue(),
	}
	ctrl := &skillAwareQueueController{skillsTestController: inner}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha", "beta"})

	require.Equal(t, http.StatusOK, scopeGet(t, srv, "/api/v1/review", token).Code)
	require.Equal(t, http.StatusOK, skillsDo(t, srv, http.MethodGet, "/api/v1/review", scopeAdminAPIKey).Code)
	assert.Equal(t, []bool{false, true}, ctrl.asked)
	assert.Zero(t, ctrl.plain, "the caller-aware read is used when the controller offers it")
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
	readSSEUntil(t, adminBody, "attention.changed", time.Now().Add(5*time.Second))
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))
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

// Review round 1, finding 1 (FR-019a timing): a scoped subscriber that
// connected before a skill became held must get NO attention.changed frame
// when the only change is that held skill. Its narrowed id set did not move,
// so a frame would tell it, by its timing alone, that a skill was held. The
// per-connection suppression is seeded at connect with the caller's current
// narrowed set. The administrator, whose set did move, gets the frame.
func TestSSE_AttentionChangedFromAHeldSkillAloneSendsAScopedSubscriberNothing(t *testing.T) {
	signIn := contracts.AttentionItem{ID: "sign_in_required:server:alpha", Kind: internalRuntime.AttentionKindSignInRequired,
		Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}}
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true,
		attentionItemsOverride: []contracts.AttentionItem{signIn}}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	readSSEUntil(t, adminBody, "attention.changed", time.Now().Add(5*time.Second))
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	baseline := readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))
	require.Equal(t, []interface{}{signIn.ID}, baseline.Data["payload"].(map[string]interface{})["ids"], "the subscriber receives its current visible ids before duplicates are suppressed")
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	held := internalRuntime.Event{Type: internalRuntime.EventTypeAttentionChanged, Timestamp: time.Now(),
		Payload: map[string]any{"count": 2, "items": []internalRuntime.AttentionEventItem{
			{ID: signIn.ID, Kind: signIn.Kind, SubjectType: "server", SubjectID: "alpha"},
			{ID: "skill_review:server:alpha:pending", Kind: internalRuntime.AttentionKindSkillReview, SubjectType: "server", SubjectID: "alpha"},
		}}}
	// A frame every subscriber that sees alpha receives, published after the
	// attention change: everything before it is what the change produced.
	sentinel := internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged, Timestamp: time.Now(),
		Payload: map[string]any{"server": "alpha"}}

	deadline := time.Now().Add(10 * time.Second)
	isSentinel := func(f sseEvent) bool { return f.Name == string(internalRuntime.EventTypeReviewChanged) }
	adminDone := make(chan []sseEvent, 1)
	go func() { adminDone <- readSSEFramesUntil(t, adminBody, deadline, isSentinel) }()
	agentDone := make(chan []sseEvent, 1)
	go func() { agentDone <- readSSEFramesUntil(t, agentBody, deadline, isSentinel) }()
	ctrl.publishToAll(held)
	ctrl.publishToAll(sentinel)

	names := func(frames []sseEvent) []string {
		var out []string
		for _, f := range frames {
			out = append(out, f.Name)
		}
		return out
	}
	assert.Contains(t, names(<-adminDone), string(internalRuntime.EventTypeAttentionChanged), "the administrator's set moved")
	assert.NotContains(t, names(<-agentDone), string(internalRuntime.EventTypeAttentionChanged),
		"a scoped subscriber whose narrowed set did not move gets no frame")
}

// Review round 2, finding 2: approving one of three pending skills keeps the
// id skill_review:server:alpha:pending but changes its count. The runtime
// publishes the change with a new content fingerprint; an administrator gets
// a frame (Home refetches the new count), a scoped subscriber, whose own ids
// did not move, still gets none.
func TestSSE_AttentionCountChangeWithStableIDsReachesOnlyAdministrators(t *testing.T) {
	tool := contracts.AttentionItem{ID: "tool_review:server:alpha:pending", Kind: internalRuntime.AttentionKindToolReview,
		Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}, Summary: "alpha: 3 new tool(s), needs review"}
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true,
		attentionItemsOverride: []contracts.AttentionItem{tool}}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	readSSEUntil(t, adminBody, "attention.changed", time.Now().Add(5*time.Second))
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	readSSEUntil(t, agentBody, "attention.changed", time.Now().Add(5*time.Second))
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	recount := tool
	recount.Summary = "alpha: 2 new tool(s), needs review"
	items := []contracts.AttentionItem{recount}
	evt := internalRuntime.Event{Type: internalRuntime.EventTypeAttentionChanged, Timestamp: time.Now(),
		Payload: map[string]any{"count": 1, "items": internalRuntime.AttentionEventItems(items),
			internalRuntime.AttentionFingerprintKey: internalRuntime.AttentionFingerprint(items)}}
	sentinel := internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged, Timestamp: time.Now(),
		Payload: map[string]any{"server": "alpha"}}

	deadline := time.Now().Add(10 * time.Second)
	isSentinel := func(f sseEvent) bool { return f.Name == string(internalRuntime.EventTypeReviewChanged) }
	adminDone := make(chan []sseEvent, 1)
	go func() { adminDone <- readSSEFramesUntil(t, adminBody, deadline, isSentinel) }()
	agentDone := make(chan []sseEvent, 1)
	go func() { agentDone <- readSSEFramesUntil(t, agentBody, deadline, isSentinel) }()
	ctrl.publishToAll(evt)
	ctrl.publishToAll(sentinel)

	count := func(frames []sseEvent) int {
		n := 0
		for _, f := range frames {
			if f.Name == string(internalRuntime.EventTypeAttentionChanged) {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 1, count(<-adminDone), "the administrator learns the count moved")
	assert.Zero(t, count(<-agentDone), "a scoped subscriber's suppression is by its own ids only")
}

// Review round 3, finding 1: an administrator narrows a token's reach while
// its SSE stream is open (re-pin, profile edit). Nothing is published for
// that, so the subscriber's suppression state still describes the old reach.
// The next attention event, here a content-only held-skill change on a server
// it can no longer see, must not reach it just because its newly narrowed ids
// differ from that stale state.
func TestSSE_ReachChangeMidStreamDoesNotLetAnInvisibleContentChangeThrough(t *testing.T) {
	item := func(kind, server, summary string) contracts.AttentionItem {
		return contracts.AttentionItem{ID: kind + ":server:" + server + ":pending", Kind: kind,
			Subject: contracts.AttentionSubject{Type: "server", ID: server, Name: server}, Summary: summary}
	}
	before := []contracts.AttentionItem{
		item(internalRuntime.AttentionKindToolReview, "alpha", "alpha: 1 new tool(s), needs review"),
		item(internalRuntime.AttentionKindToolReview, "beta", "beta: 1 new tool(s), needs review"),
		item(internalRuntime.AttentionKindSkillReview, "beta", "beta: 3 new skill(s), needs review"),
	}
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true,
		attentionItemsOverride: before}

	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	var mu sync.Mutex
	reach := []string{"alpha", "beta"}
	store := &testTokenStore{validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
		if token != rawToken {
			return nil, fmt.Errorf("token not found")
		}
		mu.Lock()
		defer mu.Unlock()
		return &auth.AgentToken{Name: "scoped-ci", TokenPrefix: auth.TokenPrefix(rawToken),
			AllowedServers: append([]string(nil), reach...), Permissions: []string{auth.PermRead},
			ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now()}, nil
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(store, tmpDir)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeBody := sseSubscribe(t, ts.URL, rawToken)
	defer closeBody()
	baseline := readSSEUntil(t, body, "attention.changed", time.Now().Add(5*time.Second))
	require.ElementsMatch(t, []interface{}{before[0].ID, before[1].ID}, baseline.Data["payload"].(map[string]interface{})["ids"])
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	// The administrator narrows the token to alpha. No event is published.
	mu.Lock()
	reach = []string{"alpha"}
	mu.Unlock()

	// One of beta's held skills is approved: same ids, new content.
	after := append([]contracts.AttentionItem(nil), before...)
	after[2].Summary = "beta: 2 new skill(s), needs review"
	ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeAttentionChanged, Timestamp: time.Now(),
		Payload: map[string]any{"count": len(after), "items": internalRuntime.AttentionEventItems(after),
			internalRuntime.AttentionFingerprintKey: internalRuntime.AttentionFingerprint(after)}})
	ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged, Timestamp: time.Now(),
		Payload: map[string]any{"server": "alpha"}})

	frames := readSSEFramesUntil(t, body, time.Now().Add(10*time.Second), func(f sseEvent) bool {
		return f.Name == string(internalRuntime.EventTypeReviewChanged)
	})
	for _, f := range frames {
		assert.NotEqual(t, string(internalRuntime.EventTypeAttentionChanged), f.Name,
			"a content change on a server outside the new reach must not produce a frame")
	}
}

// Spec 125: an exposure change sends ONE skills.changed listing the affected
// servers. skills.changed is administrator-only: the administrator gets the
// list as published, a scoped caller gets no frame at all, not even a
// narrowed list for the servers it can see.
func TestSSE_SkillsChangedListIsForAdministratorsOnly(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	deadline := time.Now().Add(10 * time.Second)
	isSentinel := func(f sseEvent) bool { return f.Name == string(internalRuntime.EventTypeReviewChanged) }
	adminDone := make(chan []sseEvent, 1)
	go func() { adminDone <- readSSEFramesUntil(t, adminBody, deadline, isSentinel) }()
	agentDone := make(chan []sseEvent, 1)
	go func() { agentDone <- readSSEFramesUntil(t, agentBody, deadline, isSentinel) }()
	for _, list := range [][]string{{"alpha", "beta"}, {"beta"}} {
		ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeSkillsChanged, Timestamp: time.Now(),
			Payload: map[string]any{"servers": list}})
	}
	ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeReviewChanged, Timestamp: time.Now(),
		Payload: map[string]any{"server": "alpha"}})

	var adminLists [][]interface{}
	for _, f := range <-adminDone {
		if f.Name == string(internalRuntime.EventTypeSkillsChanged) {
			list, _ := f.Data["payload"].(map[string]interface{})["servers"].([]interface{})
			adminLists = append(adminLists, list)
		}
	}
	assert.Equal(t, [][]interface{}{{"alpha", "beta"}, {"beta"}}, adminLists)
	for _, f := range <-agentDone {
		assert.NotEqual(t, string(internalRuntime.EventTypeSkillsChanged), f.Name, "skills.changed is administrator-only")
	}
}
