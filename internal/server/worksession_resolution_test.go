package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 124 D1: the work session of a call is resolved per request.

// trackerProxy is a storage-backed proxy whose work sessions come from a real
// tracker, so the tests see the ids production would mint.
func trackerProxy(t *testing.T) (*MCPProxyServer, *SessionStore, *runtime.WorkSessionTracker) {
	t.Helper()
	p, store := newWorkSessionProxy(t)
	tr := runtime.NewWorkSessionTracker(30 * time.Minute)
	p.workSessionResolver = tr.Resolve
	p.workSessionResolverEffective = tr.ResolveEffective
	p.workSessionToucher = tr.Touch
	return p, store, tr
}

func tokenCtx(ctx context.Context, name, prefix string) context.Context {
	return auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: name, TokenPrefix: prefix})
}

// FR-001/FR-002: a call with no MCP connection gets a work session from the
// request: principal, surface, run name.
func TestRequestWorkSession_RESTCallIsDerivedFromTheRequest(t *testing.T) {
	p, _, _ := trackerProxy(t)
	base := tokenCtx(context.Background(), "ci", "mcp_agt_aaaa")

	cli := reqcontext.WithClientSurface(base, "cli")
	rest := reqcontext.WithClientSurface(base, "rest")

	wsCLI := p.requestWorkSession(cli, "")
	require.NotEmpty(t, wsCLI, "a REST call must belong to a work session")
	assert.Equal(t, wsCLI, p.requestWorkSession(cli, ""), "the next call joins it")
	assert.NotEqual(t, wsCLI, p.requestWorkSession(rest, ""), "another surface is another client")

	named := reqcontext.WithRunHeader(rest, "nightly-7")
	wsNamed := p.requestWorkSession(named, "")
	assert.NotEqual(t, p.requestWorkSession(rest, ""), wsNamed, "a run name splits the principal's work")
	assert.Equal(t, wsNamed, p.requestWorkSession(named, ""))

	// The same run name under another token is another session (FR-008).
	other := reqcontext.WithRunHeader(reqcontext.WithClientSurface(tokenCtx(context.Background(), "ci", "mcp_agt_bbbb"), "rest"), "nightly-7")
	assert.NotEqual(t, wsNamed, p.requestWorkSession(other, ""))
}

// FR-001: a 2026-07-28 request belongs to no connection and still gets a work
// session, from its principal and the clientInfo it carries; no connection
// record is written for it (Spec 058 US3 stays).
func TestRequestWorkSession_ModernRequestIsDerivedFromTheRequest(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("stdio", "ide", "1.0", false, false, nil)

	modern := modernRequestCtx(tokenCtx(workSessionCtx("stdio"), "ci", "mcp_agt_aaaa"))
	ws := p.requestWorkSession(modern, "stdio")
	require.NotEmpty(t, ws)
	assert.Equal(t, ws, p.requestWorkSession(modernRequestCtx(tokenCtx(context.Background(), "ci", "mcp_agt_aaaa")), ""),
		"the session id of a modern request plays no part")
	assert.Equal(t, "", p.markSessionWorked(modern, "stdio"))
	assert.False(t, store.GetSession("stdio").persisted, "no connection record for a modern request")
}

// FR-006: a legacy connection keeps the id it was given, and FR-009: a second
// credential on the same connection gets its own.
func TestRequestWorkSession_LegacyConnectionKeepsItsIdPerCredential(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("c1", "ide", "1.0", false, false, nil)

	a := tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa")
	b := tokenCtx(workSessionCtx("c1"), "beta", "mcp_agt_bbbb")

	wsA := p.markSessionWorked(a, "c1")
	require.NotEmpty(t, wsA)
	assert.Equal(t, wsA, p.requestWorkSession(a, "c1"), "the row carries the connection's id")
	assert.Equal(t, wsA, store.WorkSessionID("c1"))

	wsB := p.markSessionWorked(b, "c1")
	require.NotEmpty(t, wsB)
	assert.NotEqual(t, wsA, wsB, "one connection, two credentials, two work sessions")
	assert.Equal(t, wsB, p.requestWorkSession(b, "c1"))
	assert.Equal(t, wsA, p.requestWorkSession(a, "c1"), "the first credential keeps its own")
}

// FR-009: a regenerated token (same name, new prefix) starts a new session.
func TestRequestWorkSession_RegeneratedTokenStartsANewSession(t *testing.T) {
	p, _, _ := trackerProxy(t)
	old := reqcontext.WithClientSurface(tokenCtx(context.Background(), "ci", "mcp_agt_old1"), "rest")
	regenerated := reqcontext.WithClientSurface(tokenCtx(context.Background(), "ci", "mcp_agt_new1"), "rest")
	assert.NotEqual(t, p.requestWorkSession(old, ""), p.requestWorkSession(regenerated, ""))
}

// FR-019: every call on a long-lived connection moves the idle clock, so the
// connection's id stays the live session of its identity.
func TestRequestWorkSession_EveryCallTouchesTheIdleClock(t *testing.T) {
	p, store, tr := trackerProxy(t)
	var touches atomic.Int32
	p.workSessionToucher = func(id string, identity runtime.WorkSessionIdentity) {
		touches.Add(1)
		tr.Touch(id, identity)
	}
	store.SetSession("c1", "ide", "1.0", false, false, nil)
	ctx := tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa")
	ws := p.markSessionWorked(ctx, "c1")

	for i := 0; i < 3; i++ {
		require.Equal(t, ws, p.requestWorkSession(ctx, "c1"))
	}
	assert.EqualValues(t, 3, touches.Load())
}

// FR-004: an invalid run name never fails the call; it is ignored.
func TestRequestWorkSession_InvalidRunNameIsIgnored(t *testing.T) {
	p, _, _ := trackerProxy(t)
	base := reqcontext.WithClientSurface(tokenCtx(context.Background(), "ci", "mcp_agt_aaaa"), "rest")
	bad := reqcontext.WithRunHeader(base, "two\nlines")

	assert.Equal(t, "", p.requestRunName(bad, principalFromContext(bad)))
	assert.Equal(t, p.requestWorkSession(base, ""), p.requestWorkSession(bad, ""))
}

// FR-003: the header reaches the request context on MCP over HTTP.
func TestRunNameMiddleware_CopiesTheHeader(t *testing.T) {
	var got string
	var ok bool
	h := runNameMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = reqcontext.RunHeader(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(reqcontext.RunNameHeader, "nightly-7")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.True(t, ok)
	assert.Equal(t, "nightly-7", got)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.False(t, ok)
}

// The attribution every activity funnel uses carries the request's session
// facts, so each row is stamped at the emit site.
func TestActivityAttribution_CarriesTheRequestWorkSession(t *testing.T) {
	p, _, _ := trackerProxy(t)
	ctx := reqcontext.WithRunHeader(reqcontext.WithClientSurface(
		auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "ci", TokenPrefix: "mcp_agt_aaaa", UserID: "u-1",
		}), "rest"), "nightly-7")

	attr := p.activityAttribution(ctx, "")
	assert.Equal(t, p.requestWorkSession(ctx, ""), attr.WorkSessionID)
	assert.NotEmpty(t, attr.WorkSessionID)
	assert.Equal(t, "u-1", attr.UserID)
	assert.Equal(t, "nightly-7", attr.RunName)
}

// End to end through the REST door: the persisted tool_call row of a REST call
// carries a work-session id and the run name (the gap User Story 2 closes).
func TestRESTToolCall_RowCarriesAWorkSession(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("ci-bot", "")

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var found *storage.ActivityRecord
	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.Type == storage.ActivityTypeToolCall && r.ServerName == "github" && r.ToolName == "list_issues" && r.TokenName == "ci-bot" {
				found = r
				return true
			}
		}
		return false
	}, attributionWait, 10*time.Millisecond)
	assert.NotEmpty(t, found.WorkSessionID, "a REST tool call must belong to a work session")
	assert.Empty(t, found.SessionID, "there is no MCP connection behind it")
}

// FR-040: the project of a legacy connection is its workspace basename, never
// the path; a request with no connection has none.
func TestActivityAttribution_CarriesTheProjectName(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("c1", "ide", "1.0", false, false, nil)
	store.mu.Lock()
	store.sessions["c1"].Workspace = "file:///Users/someone/repos/secret-path/my-project"
	store.mu.Unlock()

	attr := p.activityAttribution(tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa"), "c1")
	assert.Equal(t, "my-project", attr.ProjectName)

	rest := reqcontext.WithClientSurface(tokenCtx(context.Background(), "alpha", "mcp_agt_aaaa"), "rest")
	assert.Empty(t, p.activityAttribution(rest, "").ProjectName)
}

// Review finding: a run name rejected by the daily bound must not be cached
// or touched under the rejected name. The call joins the credential's unnamed
// session, and the connection's cache does not grow with invented names.
func TestRequestWorkSession_RejectedRunNamesCreateNoAliases(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("c1", "ide", "1.0", false, false, nil)
	base := tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa")
	unnamed := p.markSessionWorked(base, "c1")
	require.NotEmpty(t, unnamed)

	named := func(i int) context.Context {
		return reqcontext.WithRunHeader(base, "run-"+time.Duration(i).String())
	}
	for i := 0; i < runtime.MaxNamedWorkSessionsPerDay; i++ {
		require.NotEqual(t, unnamed, p.requestWorkSession(named(i), "c1"))
	}
	store.mu.RLock()
	admitted := len(store.sessions["c1"].workSessionByKey)
	store.mu.RUnlock()

	for i := runtime.MaxNamedWorkSessionsPerDay; i < runtime.MaxNamedWorkSessionsPerDay+50; i++ {
		// Twice each: the second call must not find an alias left by the first.
		assert.Equal(t, unnamed, p.requestWorkSession(named(i), "c1"), "a rejected name joins the unnamed session")
		assert.Equal(t, unnamed, p.requestWorkSession(named(i), "c1"))
		assert.Equal(t, unnamed, p.markSessionWorked(named(i), "c1"))
	}
	store.mu.RLock()
	after := len(store.sessions["c1"].workSessionByKey)
	store.mu.RUnlock()
	assert.Equal(t, admitted, after, "no cache entry per rejected name")
}

// Review finding: when the connection record failed to write, the retry may
// come from another credential. It must resolve its own session and never
// inherit the first credential's.
func TestRequestWorkSession_RetryAfterFailedPersistIsPerCredential(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("c1", "ide", "1.0", false, false, nil)
	a := tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa")
	b := tokenCtx(workSessionCtx("c1"), "beta", "mcp_agt_bbbb")

	wsA := p.markSessionWorked(a, "c1")
	require.NotEmpty(t, wsA)
	// What the error path of EnsurePersistedKeyed leaves behind.
	store.mu.Lock()
	store.sessions["c1"].persisted = false
	store.mu.Unlock()

	wsB := p.markSessionWorked(b, "c1")
	require.NotEmpty(t, wsB)
	assert.NotEqual(t, wsA, wsB, "the second credential gets its own work session")
	assert.Equal(t, wsA, p.markSessionWorked(a, "c1"), "the first keeps its own")
	assert.Equal(t, wsA, store.WorkSessionID("c1"), "the connection record keeps the first id")
}

// Review finding: every row of one request on a connection that has not been
// persisted (it only reads prompts) belongs to one work session, even when
// the roots reply arrives between two rows.
func TestRequestWorkSession_UnpersistedConnectionPinsOneSessionPerRequest(t *testing.T) {
	p, store, _ := trackerProxy(t)
	store.SetSession("c1", "ide", "1.0", false, false, nil)
	ctx := tokenCtx(workSessionCtx("c1"), "alpha", "mcp_agt_aaaa")

	first := p.requestWorkSession(ctx, "c1")
	require.NotEmpty(t, first)
	store.mu.Lock()
	store.sessions["c1"].Workspace = "file:///repos/late-roots"
	store.mu.Unlock()
	assert.Equal(t, first, p.requestWorkSession(ctx, "c1"), "the late workspace does not move the request to another session")

	// The first tool call persists the connection and resolves with the
	// settled workspace, as it always has (FR-006).
	worked := p.markSessionWorked(ctx, "c1")
	require.NotEmpty(t, worked)
	assert.Equal(t, worked, p.requestWorkSession(ctx, "c1"))
}

// Review finding: the display prefix has four random characters, so two
// secrets of one credential can share it. The generation tells them apart.
func TestPrincipalFromContext_UsesTheCredentialGeneration(t *testing.T) {
	mk := func(gen string) context.Context {
		return auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "ci", TokenPrefix: "mcp_agt_aaaa", TokenGeneration: gen,
		})
	}
	assert.NotEqual(t, principalFromContext(mk("gen-one")), principalFromContext(mk("gen-two")),
		"the same name and display prefix, two secrets, two principals")
	assert.Equal(t, "agent:ci:mcp_agt_aaaa", principalFromContext(mk("")), "the prefix is the fallback")
	assert.NotContains(t, principalFromContext(mk("gen-one")), "mcp_agt_aaaa")
}

// Review finding: the warning bookkeeping is bounded.
func TestRunNameWarnedSetIsBounded(t *testing.T) {
	w := &warnedSet{seen: make(map[string]struct{})}
	assert.True(t, w.first("p1"))
	assert.False(t, w.first("p1"))
	for i := 0; i < maxRunNameWarned*3; i++ {
		w.first("principal-" + time.Duration(i).String())
	}
	w.mu.Lock()
	n := len(w.seen)
	w.mu.Unlock()
	assert.LessOrEqual(t, n, maxRunNameWarned)
}
