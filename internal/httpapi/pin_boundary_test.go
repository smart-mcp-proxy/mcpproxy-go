package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 118 / issue #1686: the operator-set profile pin is a visibility
// boundary on every REST and SSE read door, not only on /servers and /tools.
// Fleet github / notion / filesystem; pin work-readonly = [github]; the token's
// grant is "*", so before the fix every door but /servers and /tools listed
// the whole fleet to it.

// pinBoundaryController adds the review reader the pin fixture lacks, so
// GET /api/v1/review is driven through the production handler.
type pinBoundaryController struct {
	*callerPinController
	activities []*storage.ActivityRecord
}

// The activity door takes the grant as a storage filter (scopeAllowedServers)
// rather than calling the predicate per row; the production
// storage.ActivityFilter.Matches is what applies it here.
func (c *pinBoundaryController) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	filter.Validate()
	var matched []*storage.ActivityRecord
	for _, a := range c.activities {
		if filter.Matches(a) {
			matched = append(matched, a)
		}
	}
	total := len(matched)
	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	return matched, total, nil
}

func (c *pinBoundaryController) GetReviewQueue(context.Context) (*internalRuntime.ReviewQueue, error) {
	rows := []internalRuntime.ReviewQueueRow{}
	for i := range c.servers {
		if c.servers[i].Quarantined {
			rows = append(rows, internalRuntime.ReviewQueueRow{Server: c.servers[i].Name, Kind: "server_review", Quarantined: true})
		}
	}
	return &internalRuntime.ReviewQueue{Count: len(rows), Servers: rows}, nil
}

func (c *pinBoundaryController) GetServerReview(_ context.Context, name string) (*internalRuntime.ServerReview, error) {
	for i := range c.servers {
		if c.servers[i].Name == name {
			return &internalRuntime.ServerReview{}, nil
		}
	}
	return nil, internalRuntime.ErrReviewServerNotFound
}

type pinBoundaryFixture struct {
	srv      *Server
	ctrl     *pinBoundaryController
	cfg      *config.Config
	pinned   string // "*"-granted token pinned to work-readonly
	legacy   string // --servers github token, no pin
	deleted  string // "*"-granted token pinned to a profile that does not exist
	disjoint string // [github]-granted token pinned to notion-only (no overlap)
	empty    string // []-granted token, no pin (the FR-002 comparison shape)
	star     string // "*"-granted token, no pin (the usage-cache neighbour)
}

func pinBoundaryServer(t *testing.T, withManagement bool) *pinBoundaryFixture {
	t.Helper()
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		APIKey: scopeAdminAPIKey,
		Servers: []*config.ServerConfig{
			{Name: "github", Enabled: true}, {Name: "notion", Enabled: true}, {Name: "filesystem", Enabled: true},
		},
		Profiles: []config.ProfileConfig{
			{Name: "work-readonly", Servers: []string{"github"}, SwitchableTo: &[]string{"work-full"}},
			{Name: "work-full", Servers: []string{"github", "notion", "filesystem"}},
			{Name: "notion-only", Servers: []string{"notion"}},
		},
	}
	servers := []contracts.Server{
		{ID: "github", Name: "github", Enabled: true, Connected: true, ToolCount: 2},
		{ID: "notion", Name: "notion", Enabled: true, Connected: true, Quarantined: true, ToolCount: 2,
			Quarantine: &contracts.QuarantineStats{PendingCount: 1}},
		{ID: "filesystem", Name: "filesystem", Enabled: true, Connected: true, ToolCount: 1},
	}
	ctrl := &pinBoundaryController{callerPinController: &callerPinController{scopeController: scopeController{cfg: cfg, servers: servers, withManagement: withManagement, statusCh: make(chan interface{}, 4)}}}
	now := time.Now().UTC()
	for i, server := range []string{"github", "notion", "filesystem", "github"} {
		ctrl.activities = append(ctrl.activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("act-%d", i), Type: storage.ActivityTypeToolCall, ServerName: server, ToolName: "t",
			Status: storage.ActivityStatusSuccess, Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	mint := func(name string, grant []string, pin string) (string, *auth.AgentToken) {
		raw, err := auth.GenerateToken()
		require.NoError(t, err)
		return raw, &auth.AgentToken{
			Name: name, TokenPrefix: auth.TokenPrefix(raw), AllowedServers: grant,
			Permissions: []string{auth.PermRead}, ProfilePin: pin,
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		}
	}
	pinnedRaw, pinned := mint("pinned", []string{"*"}, "work-readonly")
	legacyRaw, legacy := mint("legacy", []string{"github"}, "")
	deletedRaw, deleted := mint("deleted", []string{"*"}, "gone")
	disjointRaw, disjoint := mint("disjoint", []string{"github"}, "notion-only")
	emptyRaw, empty := mint("empty", []string{}, "")
	starRaw, star := mint("star", []string{"*"}, "")
	tokens := map[string]*auth.AgentToken{pinnedRaw: pinned, legacyRaw: legacy, deletedRaw: deleted, disjointRaw: disjoint, emptyRaw: empty, starRaw: star}
	store := &testTokenStore{validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
		if tok, ok := tokens[token]; ok {
			return tok, nil
		}
		return nil, fmt.Errorf("token not found")
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(store, tmpDir)
	return &pinBoundaryFixture{srv: srv, ctrl: ctrl, cfg: cfg, pinned: pinnedRaw, legacy: legacyRaw, deleted: deletedRaw, disjoint: disjointRaw, empty: emptyRaw, star: starRaw}
}

func (f *pinBoundaryFixture) get(t *testing.T, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	return scopeGet(t, f.srv, path, key)
}

func (f *pinBoundaryFixture) data(t *testing.T, path, key string) map[string]interface{} {
	t.Helper()
	rec := f.get(t, path, key)
	require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	return scopeDecodeData(t, rec)
}

// names collects every string that equals a server name anywhere in a decoded
// JSON value: keys of maps, string leaves, `name`/`server` fields alike.
func namesIn(v interface{}, fleet []string, found map[string]bool) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			for _, n := range fleet {
				if k == n {
					found[n] = true
				}
			}
			namesIn(val, fleet, found)
		}
	case []interface{}:
		for _, val := range x {
			namesIn(val, fleet, found)
		}
	case string:
		for _, n := range fleet {
			if x == n {
				found[n] = true
			}
		}
	}
}

var pinFleet = []string{"github", "notion", "filesystem"}

func assertOnlyGithub(t *testing.T, label string, body interface{}) {
	t.Helper()
	found := map[string]bool{}
	namesIn(body, pinFleet, found)
	assert.False(t, found["notion"], "%s names notion (outside the pin): %v", label, body)
	assert.False(t, found["filesystem"], "%s names filesystem (outside the pin): %v", label, body)
}

func TestPinBoundary_StatusNarrowsToThePin(t *testing.T) {
	for _, mgmt := range []bool{true, false} {
		t.Run(fmt.Sprintf("management=%v", mgmt), func(t *testing.T) {
			f := pinBoundaryServer(t, mgmt)
			data := f.data(t, "/api/v1/status", f.pinned)
			stats := data["upstream_stats"].(map[string]interface{})
			servers := stats["servers"].(map[string]interface{})
			assert.Equal(t, []string{"github"}, mapKeys(servers))
			assert.Equal(t, float64(1), stats["total_servers"])
			assert.Equal(t, float64(1), stats["connected_servers"])
			assert.Equal(t, float64(0), stats["quarantined_servers"])
			assert.Equal(t, float64(2), stats["total_tools"])
			assertOnlyGithub(t, "GET /status", data)

			// Unchanged for the legacy --servers token and the administrator.
			legacy := f.data(t, "/api/v1/status", f.legacy)["upstream_stats"].(map[string]interface{})
			assert.Equal(t, []string{"github"}, mapKeys(legacy["servers"].(map[string]interface{})))
			admin := f.data(t, "/api/v1/status", scopeAdminAPIKey)["upstream_stats"].(map[string]interface{})
			assert.Len(t, admin["servers"].(map[string]interface{}), 3)
			assert.Equal(t, float64(3), admin["total_servers"])
		})
	}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestPinBoundary_CoverageReviewAttentionNarrowToThePin(t *testing.T) {
	f := pinBoundaryServer(t, true)

	coverage := f.data(t, "/api/v1/annotations/coverage", f.pinned)
	rows := coverage["servers"].([]interface{})
	require.Len(t, rows, 1)
	assert.Equal(t, "github", rows[0].(map[string]interface{})["name"])
	assert.Equal(t, float64(2), coverage["total_tools"])
	assertOnlyGithub(t, "GET /annotations/coverage", coverage)

	// notion is the only quarantined server, so the queue names it alone:
	// the pinned token must see an empty queue and a zero count.
	review := f.data(t, "/api/v1/review", f.pinned)
	assert.Equal(t, float64(0), review["count"])
	assert.Empty(t, review["servers"])
	adminReview := f.data(t, "/api/v1/review", scopeAdminAPIKey)
	assert.Equal(t, float64(1), adminReview["count"], "administrator view unchanged")

	attention := f.data(t, "/api/v1/attention", f.pinned)
	assertOnlyGithub(t, "GET /attention", attention)
	items, _ := attention["items"].([]interface{})
	assert.Equal(t, float64(len(items)), attention["count"])
	adminAttention := f.data(t, "/api/v1/attention", scopeAdminAPIKey)
	found := map[string]bool{}
	namesIn(adminAttention, pinFleet, found)
	assert.True(t, found["notion"], "precondition: the administrator's attention list names the quarantined notion")
}

func TestPinBoundary_ProfileDoorsListOnlyTheOwnPin(t *testing.T) {
	f := pinBoundaryServer(t, true)

	list := f.data(t, "/api/v1/profiles", f.pinned)
	profiles := list["profiles"].([]interface{})
	require.Len(t, profiles, 1, "a pinned token may learn no profile but its own: %v", profiles)
	own := profiles[0].(map[string]interface{})
	assert.Equal(t, "work-readonly", own["name"])
	assert.Equal(t, []interface{}{"github"}, own["servers"])
	assert.Equal(t, []interface{}{"github"}, own["effective_servers"])
	assert.Empty(t, own["switchable_to"], "switchable_to names profiles; cleared for a pinned caller")
	assertOnlyGithub(t, "GET /profiles", list)

	// work-full contains github, so Spec 107 reachability would list it for
	// an unpinned scoped token; the pinned token gets the unknown-profile answer.
	full := f.get(t, "/api/v1/profiles/work-full", f.pinned)
	ghost := f.get(t, "/api/v1/profiles/ghost", f.pinned)
	assert.Equal(t, http.StatusNotFound, full.Code)
	assert.Equal(t, ghost.Code, full.Code)
	assert.Equal(t, scopeDecodeError(t, ghost), scopeDecodeError(t, full), "existing-but-not-pin and unknown must answer the same body")

	fullTools := f.get(t, "/api/v1/profiles/work-full/effective-tools", f.pinned)
	ghostTools := f.get(t, "/api/v1/profiles/ghost/effective-tools", f.pinned)
	assert.Equal(t, http.StatusNotFound, fullTools.Code)
	assert.Equal(t, scopeDecodeError(t, ghostTools), scopeDecodeError(t, fullTools))

	ownView := f.data(t, "/api/v1/profiles/work-readonly", f.pinned)
	assert.Equal(t, []interface{}{"github"}, ownView["servers"])
	// The own pin passes the reachability gate on /effective-tools (the
	// config-only profiles reader behind this fixture has no evaluator, so
	// the answer is 503, never the 404 every other name gets).
	assert.Equal(t, http.StatusServiceUnavailable, f.get(t, "/api/v1/profiles/work-readonly/effective-tools", f.pinned).Code)

	// A --servers github token keeps the Spec 107 T085 rule: every profile
	// its grant reaches, narrowed.
	legacyList := f.data(t, "/api/v1/profiles", f.legacy)["profiles"].([]interface{})
	names := map[string]bool{}
	for _, p := range legacyList {
		names[p.(map[string]interface{})["name"].(string)] = true
	}
	assert.Equal(t, map[string]bool{"work-readonly": true, "work-full": true}, names)
}

func TestPinBoundary_ActiveProfileReadsOnlyASelectableSlug(t *testing.T) {
	f := pinBoundaryServer(t, true)
	setActive := func(slug string) {
		f.srv.activeProfileMu.Lock()
		f.srv.activeProfile = slug
		f.srv.activeProfileMu.Unlock()
	}

	setActive("work-full")
	assert.Equal(t, "", f.data(t, "/api/v1/profiles/active", f.pinned)["active_profile"],
		"a pinned token never reads a slug other than its own pin")
	assert.Equal(t, "work-full", f.data(t, "/api/v1/profiles/active", f.legacy)["active_profile"],
		"work-full intersects the --servers github grant (Spec 107 T085)")
	assert.Equal(t, "work-full", f.data(t, "/api/v1/profiles/active", scopeAdminAPIKey)["active_profile"])

	setActive("work-readonly")
	assert.Equal(t, "work-readonly", f.data(t, "/api/v1/profiles/active", f.pinned)["active_profile"],
		"the own pin with visible reach is readable")

	// FR-004: the one deliberate change for unpinned scoped callers — an
	// active profile disjoint from the grant reads "" instead of its slug.
	setActive("notion-only")
	assert.Equal(t, "", f.data(t, "/api/v1/profiles/active", f.legacy)["active_profile"])
	assert.Equal(t, "", f.data(t, "/api/v1/profiles/active", f.pinned)["active_profile"])
	assert.Equal(t, "notion-only", f.data(t, "/api/v1/profiles/active", scopeAdminAPIKey)["active_profile"])
}

func TestPinBoundary_ViewAsMayNameOnlyTheOwnPin(t *testing.T) {
	f := pinBoundaryServer(t, true)

	bare := f.get(t, "/api/v1/servers", f.pinned)
	own := f.get(t, "/api/v1/servers?profile=work-readonly", f.pinned)
	require.Equal(t, http.StatusOK, bare.Code)
	assert.Equal(t, bare.Body.String(), own.Body.String(), "?profile=<own pin> equals the bare listing")
	assertOnlyGithub(t, "GET /servers", scopeDecodeData(t, bare))

	wide := f.get(t, "/api/v1/servers?profile=work-full", f.pinned)
	ghost := f.get(t, "/api/v1/servers?profile=ghost", f.pinned)
	assert.Equal(t, http.StatusNotFound, wide.Code, "a pinned token may select only its own pin")
	assert.Equal(t, scopeDecodeError(t, ghost), scopeDecodeError(t, wide))

	wideTools := f.get(t, "/api/v1/tools?profile=work-full", f.pinned)
	ghostTools := f.get(t, "/api/v1/tools?profile=ghost", f.pinned)
	assert.Equal(t, http.StatusNotFound, wideTools.Code)
	assert.Equal(t, scopeDecodeError(t, ghostTools), scopeDecodeError(t, wideTools))

	// Unpinned scoped callers keep the narrowing rule: a reachable profile
	// is viewable and only narrows.
	legacyWide := f.get(t, "/api/v1/servers?profile=work-full", f.legacy)
	assert.Equal(t, http.StatusOK, legacyWide.Code)
	assertOnlyGithub(t, "legacy GET /servers?profile=work-full", scopeDecodeData(t, legacyWide))
}

// scopeDecodeError returns a refusal envelope re-encoded without the
// per-request request_id, so two refusals can be compared whole.
func scopeDecodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	delete(envelope, "request_id")
	out, err := json.Marshal(envelope)
	require.NoError(t, err)
	return string(out)
}

func TestPinBoundary_SubtreeAnswersLikeAMissingServer(t *testing.T) {
	f := pinBoundaryServer(t, true)
	for _, path := range []string{"/tools", "/review", "/diagnostics", "/logs", "/tool-calls"} {
		hidden := f.get(t, "/api/v1/servers/notion"+path, f.pinned)
		missing := f.get(t, "/api/v1/servers/ghost"+path, f.pinned)
		assert.Equal(t, http.StatusNotFound, hidden.Code, path)
		assert.Equal(t, strings.ReplaceAll(scopeDecodeError(t, missing), "ghost", "notion"), scopeDecodeError(t, hidden), path)
	}
	assert.Equal(t, http.StatusOK, f.get(t, "/api/v1/servers/github/tools", f.pinned).Code)
}

func TestPinBoundary_GrantAsFilterSeamAndDiagnostics(t *testing.T) {
	f := pinBoundaryServer(t, true)

	// The grant-as-filter seam hands storage the resolved visible set, never
	// the raw "*" (FR-019).
	var seen []string
	var scoped bool
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
	req.Header.Set("X-API-Key", f.pinned)
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, scoped = scopeAllowedServers(r.Context())
	})
	f.srv.apiKeyAuthMiddleware()(probe).ServeHTTP(httptest.NewRecorder(), req)
	assert.True(t, scoped)
	assert.Equal(t, []string{"github"}, seen)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
	req.Header.Set("X-API-Key", f.deleted)
	f.srv.apiKeyAuthMiddleware()(probe).ServeHTTP(httptest.NewRecorder(), req)
	assert.True(t, scoped)
	assert.NotNil(t, seen, "a deleted pin yields an EMPTY, non-nil set: nil reads as unrestricted downstream")
	assert.Empty(t, seen)

	diag := f.data(t, "/api/v1/diagnostics", f.pinned)
	assertOnlyGithub(t, "GET /diagnostics", diag)
	errs, _ := diag["upstream_errors"].([]interface{})
	assert.Len(t, errs, 1)

	// The activity door (L8): records for notion and filesystem are filtered
	// inside the query, so `total` and the page describe the same set.
	activity := f.data(t, "/api/v1/activity", f.pinned)
	assertOnlyGithub(t, "GET /activity", activity)
	assert.Equal(t, float64(2), activity["total"])
	assert.Len(t, activity["activities"], 2)
	assert.Equal(t, float64(4), f.data(t, "/api/v1/activity", scopeAdminAPIKey)["total"], "administrator view unchanged")
	assert.Equal(t, float64(0), f.data(t, "/api/v1/activity", f.deleted)["total"], "a deleted pin reads no records")

	// The usage cache key (FR-019) is built from the resolved set: a pinned
	// "*" token never shares an entry with an unpinned "*" token.
	keyFor := func(token string) string {
		var key string
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/usage", http.NoBody)
		req.Header.Set("X-API-Key", token)
		f.srv.apiKeyAuthMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			p, err := parseUsageParams(r)
			require.NoError(t, err)
			key = p.cacheKey()
		})).ServeHTTP(httptest.NewRecorder(), req)
		require.NotEmpty(t, key)
		return key
	}
	assert.NotEqual(t, keyFor(f.star), keyFor(f.pinned), "pinned and unpinned * tokens must not share a usage cache entry")
	assert.NotEqual(t, keyFor(f.pinned), keyFor(f.deleted), "a deleted pin's entry is not the live pin's")
}

func TestPinBoundary_DeletedPinHidesEverything(t *testing.T) {
	f := pinBoundaryServer(t, true)
	f.srv.activeProfileMu.Lock()
	f.srv.activeProfile = "gone"
	f.srv.activeProfileMu.Unlock()

	// Every door answers the empty-grant shape (FR-002): compare against a
	// token whose grant is [] and has no pin.
	for _, path := range []string{"/api/v1/status", "/api/v1/servers", "/api/v1/profiles", "/api/v1/review",
		"/api/v1/attention", "/api/v1/annotations/coverage", "/api/v1/tools", "/api/v1/diagnostics"} {
		deleted := scopeDecodeData(t, f.get(t, path, f.deleted))
		empty := scopeDecodeData(t, f.get(t, path, f.empty))
		for _, volatile := range []string{"timestamp", "generated_at", "started_at"} {
			delete(deleted, volatile)
			delete(empty, volatile)
		}
		assert.Equal(t, empty, deleted, "%s: a deleted pin must look exactly like an empty grant", path)
		found := map[string]bool{}
		namesIn(deleted, pinFleet, found)
		assert.Empty(t, found, "%s names a server under a deleted pin: %v", path, deleted)
	}

	for _, path := range []string{"/api/v1/servers?profile=gone", "/api/v1/profiles/gone", "/api/v1/profiles/gone/effective-tools"} {
		assert.Equal(t, http.StatusNotFound, f.get(t, path, f.deleted).Code, path)
	}
	assert.Equal(t, "", f.data(t, "/api/v1/profiles/active", f.deleted)["active_profile"])

	// A pin whose profile exists but has no overlap with the grant is the
	// same as a deleted one (FR-002): grant [github] pinned to notion-only.
	for _, path := range []string{"/api/v1/status", "/api/v1/servers", "/api/v1/profiles", "/api/v1/tools"} {
		disjoint := scopeDecodeData(t, f.get(t, path, f.disjoint))
		empty := scopeDecodeData(t, f.get(t, path, f.empty))
		for _, volatile := range []string{"timestamp", "generated_at", "started_at"} {
			delete(disjoint, volatile)
			delete(empty, volatile)
		}
		assert.Equal(t, empty, disjoint, "%s: a pin with no overlap must look exactly like an empty grant", path)
	}
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/v1/profiles/notion-only", f.disjoint).Code)
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/v1/servers?profile=notion-only", f.disjoint).Code)
}

func TestPinBoundary_SSEFramesHoldToThePin(t *testing.T) {
	f := pinBoundaryServer(t, true)
	ts := httptest.NewServer(f.srv)
	defer ts.Close()

	body, closeStream := sseSubscribe(t, ts.URL, f.pinned)
	defer closeStream()
	require.Eventually(t, func() bool { return f.ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	now := time.Now()
	embed := []contracts.Server{
		{ID: "github", Name: "github", Enabled: true, Connected: true, ToolCount: 2},
		{ID: "notion", Name: "notion", Enabled: true, Connected: true, ToolCount: 2},
		{ID: "filesystem", Name: "filesystem", Enabled: true, Connected: true, ToolCount: 1},
	}
	// 1. A server-named audit event about notion is dropped.
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeActivityToolCallCompleted,
		Payload: map[string]any{"server_name": "notion", "tool_name": "read_page", "status": "ok"}, Timestamp: now})
	// 2. A servers.changed whose embed is hidden-only and whose extra names a
	//    hidden server is dropped (FR-007).
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged,
		Payload: map[string]any{"servers": embed[1:], "stats": &contracts.ServerStats{TotalServers: 2}, "server": "notion", "reason": "restart"}, Timestamp: now})
	// 3. A coalesced servers.changed with the whole fleet reaches the token
	//    with the embed narrowed and the hidden extra withheld.
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged,
		Payload: map[string]any{"servers": embed, "stats": &contracts.ServerStats{TotalServers: 3, ConnectedServers: 3, TotalTools: 5}, "server": "notion", "reason": "tools_changed"}, Timestamp: now})
	// Sentinel: an event about github is delivered.
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeActivityToolCallCompleted,
		Payload: map[string]any{"server_name": "github", "tool_name": "read_issue", "status": "ok"}, Timestamp: now})

	frames := readSSEFramesUntil(t, body, time.Now().Add(10*time.Second), func(e sseEvent) bool {
		p, _ := e.Data["payload"].(map[string]interface{})
		return e.Name == string(internalRuntime.EventTypeActivityToolCallCompleted) && p["server_name"] == "github"
	})
	var changed []map[string]interface{}
	for _, fr := range frames {
		assertOnlyGithub(t, "SSE "+fr.Name, fr.Data)
		if fr.Name == "servers.changed" {
			p, _ := fr.Data["payload"].(map[string]interface{})
			changed = append(changed, p)
		}
	}
	require.Len(t, changed, 1, "the hidden-only servers.changed is dropped; the coalesced one is rendered")
	rows := changed[0]["servers"].([]interface{})
	require.Len(t, rows, 1)
	assert.Equal(t, "github", rows[0].(map[string]interface{})["name"])
	assert.NotContains(t, changed[0], "server")
	stats := changed[0]["stats"].(map[string]interface{})
	assert.Equal(t, float64(1), stats["total_servers"])
	assert.Equal(t, float64(2), stats["total_tools"])

	// A hidden server's change COUNTS go with its name (round 1 MUST-FIX):
	// a tools_changed for notion with a fleet embed reaches the token with
	// github's row, `reason`, `stats` — and nothing else.
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged,
		Payload: map[string]any{"servers": embed, "stats": &contracts.ServerStats{TotalServers: 3}, "server": "notion", "reason": "tools_changed", "added": 3, "modified": 1, "removed": 0}, Timestamp: now})
	got := readSSEUntil(t, body, "servers.changed", time.Now().Add(10*time.Second))
	p := got.Data["payload"].(map[string]interface{})
	assert.ElementsMatch(t, []string{"servers", "stats", "reason"}, mapKeys(p), "%v", p)

	// active_profile.changed is the push twin of GET /profiles/active: a slug
	// the token may not select arrives as "".
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeActiveProfileChanged,
		Payload: map[string]any{"active_profile": "work-full"}, Timestamp: now})
	got = readSSEUntil(t, body, "active_profile.changed", time.Now().Add(10*time.Second))
	assert.Equal(t, "", got.Data["payload"].(map[string]interface{})["active_profile"])
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeActiveProfileChanged,
		Payload: map[string]any{"active_profile": "work-readonly"}, Timestamp: now})
	got = readSSEUntil(t, body, "active_profile.changed", time.Now().Add(10*time.Second))
	assert.Equal(t, "work-readonly", got.Data["payload"].(map[string]interface{})["active_profile"], "the own pin with visible reach is readable")

	// A live status frame narrows upstream_stats to the pin, counters included.
	f.ctrl.statusCh <- map[string]interface{}{"phase": "Ready"}
	got = readSSEUntil(t, body, "status", time.Now().Add(10*time.Second))
	up := got.Data["upstream_stats"].(map[string]interface{})
	assert.Equal(t, []string{"github"}, mapKeys(up["servers"].(map[string]interface{})))
	assert.Equal(t, float64(1), up["total_servers"])
	assert.Equal(t, float64(2), up["total_tools"])

	// Live pin deletion (FR-003): the reach is re-resolved before every frame,
	// so once the pinned profile is gone a fleet frame carries nothing visible
	// and is dropped; an identity-less notify frame still arrives, empty, and
	// a status frame carries an empty server map and zero counters.
	f.cfg.Profiles = f.cfg.Profiles[1:]
	f.ctrl.statusCh <- map[string]interface{}{"phase": "Ready"}
	got = readSSEUntil(t, body, "status", time.Now().Add(10*time.Second))
	up = got.Data["upstream_stats"].(map[string]interface{})
	assert.Empty(t, up["servers"])
	assert.Equal(t, float64(0), up["total_servers"])
	assert.Equal(t, float64(0), up["total_tools"])
	assertOnlyGithub(t, "post-deletion status", got.Data)
	found := map[string]bool{}
	namesIn(got.Data, pinFleet, found)
	assert.Empty(t, found)
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged,
		Payload: map[string]any{"servers": embed, "stats": &contracts.ServerStats{TotalServers: 3}, "server": "github", "reason": "restart"}, Timestamp: now})
	f.ctrl.publishToAll(internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged,
		Payload: map[string]any{"reason": "reload"}, Timestamp: now})
	frames = readSSEFramesUntil(t, body, time.Now().Add(10*time.Second), func(e sseEvent) bool {
		p, _ := e.Data["payload"].(map[string]interface{})
		return e.Name == "servers.changed" && p["reason"] == "reload"
	})
	for _, fr := range frames {
		found := map[string]bool{}
		namesIn(fr.Data, pinFleet, found)
		assert.Empty(t, found, "after the pin's profile is deleted no frame names any server: %v", fr.Data)
		if p, _ := fr.Data["payload"].(map[string]interface{}); fr.Name == "servers.changed" {
			assert.Equal(t, "reload", p["reason"], "the fleet frame about github is dropped once github is outside the (now empty) visible set")
		}
	}
}

// TestPinBoundary_ProfilesDoorWithoutPinReachListsNothing: a pin that exists
// but is empty has no visible reach, so its own profile is not listed either
// (the token cannot tell an empty pin from a deleted one).
func TestPinBoundary_EmptyPinListsNoProfile(t *testing.T) {
	f := pinBoundaryServer(t, true)
	f.cfg.Profiles[0].Servers = nil
	assert.Empty(t, f.data(t, "/api/v1/profiles", f.pinned)["profiles"])
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/v1/profiles/work-readonly", f.pinned).Code)
	assert.Equal(t, http.StatusNotFound, f.get(t, "/api/v1/servers?profile=work-readonly", f.pinned).Code)
	assert.Empty(t, f.data(t, "/api/v1/servers", f.pinned)["servers"])
}

// The servers.changed drop rule in isolation (FR-007): dropped only when the
// narrowed embed is empty AND every identity extra names a hidden server.
func TestServersChangedDeliverable(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"github"}, ProfilePin: "p"})
	unpinned := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"github"}})
	evt := func(payload map[string]any) internalRuntime.Event {
		return internalRuntime.Event{Type: internalRuntime.EventTypeServersChanged, Payload: payload}
	}
	github := contracts.Server{Name: "github"}
	notion := contracts.Server{Name: "notion"}

	assert.False(t, eventVisibleToCaller(ctx, evt(map[string]any{"servers": []contracts.Server{notion}, "server": "notion"})), "hidden-only embed + hidden extra")
	assert.False(t, eventVisibleToCaller(ctx, evt(map[string]any{"server": "notion"})), "notify-only naming a hidden server")
	assert.True(t, eventVisibleToCaller(ctx, evt(map[string]any{"servers": []contracts.Server{github, notion}, "server": "notion"})), "visible row survives")
	assert.True(t, eventVisibleToCaller(ctx, evt(map[string]any{"servers": []contracts.Server{notion}, "server": "github"})), "visible identity survives")
	assert.True(t, eventVisibleToCaller(ctx, evt(map[string]any{"reason": "reload"})), "identity-less notify-only is the accepted residual")
	assert.True(t, eventVisibleToCaller(ctx, evt(map[string]any{"servers": []contracts.Server{}})), "empty embed with no identity")
	assert.True(t, eventVisibleToCaller(auth.WithAuthContext(context.Background(), auth.AdminContext()), evt(map[string]any{"servers": []contracts.Server{notion}, "server": "notion"})), "admin sees everything")
	assert.True(t, eventVisibleToCaller(unpinned, evt(map[string]any{"servers": []contracts.Server{notion}, "server": "notion"})), "an unpinned scoped caller keeps the #1166 delivery (FR-004); the name and its extras are withheld at render time")
}
