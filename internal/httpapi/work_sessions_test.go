package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 124 D6: the work-session doors, through the production route table and
// the real agent-token middleware, over real storage.

// wsController is the scope fixture's controller plus real work-session
// storage.
type wsController struct {
	*scopeController
	store *storage.Manager
}

func (c *wsController) ListWorkSessionSummaries() ([]*storage.WorkSessionSummary, error) {
	return c.store.ListWorkSessionSummaries()
}
func (c *wsController) GetWorkSessionSummary(id string) (*storage.WorkSessionSummary, error) {
	return c.store.GetWorkSessionSummary(id)
}
func (c *wsController) WorkSessionRowRefs(id string) ([]storage.WorkSessionRowRef, error) {
	return c.store.WorkSessionRowRefs(id)
}
func (c *wsController) UpstreamEventRefs(server string, from, to time.Time) ([]storage.WorkSessionRowRef, error) {
	return c.store.UpstreamEventRefs(server, from, to)
}
func (c *wsController) GetActivitiesByKeys(keys [][]byte) ([]*storage.ActivityRecord, error) {
	return c.store.GetActivitiesByKeys(keys)
}

const (
	wsAlphaSecretArg  = "ALPHA_ARG_BODY"
	wsBetaSecret      = "BETA_HIDDEN_PAYLOAD"
	wsFailedAlphaID   = "01WSALPHAFAILED00000000000"
	wsFailedBetaID    = "01WSBETAFAILED000000000000"
	wsOwnedEventOwner = "alice"
)

type wsFixture struct {
	t      *testing.T
	srv    *Server
	token  string
	prefix string
	store  *storage.Manager
	base   time.Time
}

// newWSFixture builds a server whose scoped token may see alpha only, and
// seeds:
//
//   - ws-mine: the token's own session, with rows on alpha (in reach), on beta
//     (hidden) and on MCPProxy's own tools, and upstream events on both servers;
//   - ws-other: another token's session on alpha;
//   - ws-hidden: the token's own session that touched only beta.
func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	store, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctrl := &wsController{
		scopeController: &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true},
		store:           store,
	}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	f := &wsFixture{t: t, srv: srv, token: token, prefix: auth.TokenPrefix(token), store: store, base: time.Now().UTC().Add(-10 * time.Minute)}

	mine := func(offset time.Duration, mutate func(*storage.ActivityRecord)) {
		f.row("ws-mine", "scoped-ci", f.prefix, offset, mutate)
	}
	mine(0, nil)
	mine(time.Minute, func(r *storage.ActivityRecord) {
		r.ID, r.Status, r.ErrorClass, r.ErrorMessage = wsFailedAlphaID, storage.ActivityStatusError, "network", "connection reset"
		r.Arguments, r.Response = map[string]interface{}{"q": wsAlphaSecretArg}, "alpha response"
		r.Profile, r.ProfileSource, r.SessionID = "work-full", "pin", "conn-1"
	})
	mine(2*time.Minute, func(r *storage.ActivityRecord) {
		r.ID, r.ServerName, r.ToolName = wsFailedBetaID, "beta", "beta_tool"
		r.Status, r.ErrorMessage, r.Response = storage.ActivityStatusError, wsBetaSecret, wsBetaSecret
	})
	mine(3*time.Minute, func(r *storage.ActivityRecord) {
		r.Type, r.ServerName, r.ToolName = storage.ActivityTypeInternalToolCall, "", "retrieve_tools"
	})

	f.row("ws-other", "someone-else", "mcp_agt_zzzz", 30*time.Second, nil)
	f.row("ws-hidden", "scoped-ci", f.prefix, 4*time.Minute, func(r *storage.ActivityRecord) { r.ServerName, r.ToolName = "beta", "beta_tool" })

	f.event("alpha", 30*time.Second, "")
	f.event("alpha", 40*time.Second, wsOwnedEventOwner)
	f.event("beta", 90*time.Second, "")
	f.event("alpha", -time.Hour, "") // before the session: outside its span
	return f
}

func (f *wsFixture) row(ws, tokenName, prefix string, offset time.Duration, mutate func(*storage.ActivityRecord)) {
	f.t.Helper()
	r := &storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, Status: storage.ActivityStatusSuccess, ServerName: "alpha", ToolName: "alpha_tool",
		Timestamp: f.base.Add(offset), WorkSessionID: ws, TokenName: tokenName, TokenPrefix: prefix,
		ClientName: "rest", Source: storage.ActivitySourceAPI, RunName: "nightly",
	}
	if mutate != nil {
		mutate(r)
	}
	require.NoError(f.t, f.store.SaveActivity(r))
}

func (f *wsFixture) event(server string, offset time.Duration, owner string) {
	f.t.Helper()
	require.NoError(f.t, f.store.SaveActivity(&storage.ActivityRecord{
		Type: storage.ActivityTypeUpstreamEvent, Status: storage.ActivityStatusError, ServerName: server,
		ErrorMessage: "The connection to the upstream server was lost.", Timestamp: f.base.Add(offset), UserID: owner,
		Source: storage.ActivitySourceInternal, Metadata: map[string]interface{}{"kind": "connection_lost"},
	}))
}

func wsData(t *testing.T, rec *httptest.ResponseRecorder, into interface{}) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	require.NoError(t, json.Unmarshal(env.Data, into), string(env.Data))
}

var wsRequestID = regexp.MustCompile(`"request_id":"[^"]*"`)

func wsNormalize(rec *httptest.ResponseRecorder) string {
	return wsRequestID.ReplaceAllString(rec.Body.String(), `"request_id":"<id>"`)
}

// FR-046/FR-048/SC-008: an administrator sees every session whole; an agent
// token sees only its own, and only the part on servers in its reach.
func TestWorkSessions_Authorization(t *testing.T) {
	f := newWSFixture(t)

	var admin contracts.WorkSessionListResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions", scopeAdminAPIKey), &admin)
	require.Equal(t, 3, admin.Total)
	byID := map[string]contracts.WorkSession{}
	for _, s := range admin.Sessions {
		byID[s.ID] = s
	}
	whole := byID["ws-mine"]
	assert.Equal(t, 4, whole.Attempts, "alpha x2, beta, and the builtin")
	assert.Equal(t, 2, whole.Failures)
	assert.Equal(t, "work-full", whole.Profile, "an administrator sees the profile")
	assert.Equal(t, 1, whole.ConnectionCount)
	require.NotNil(t, whole.LastFailure)
	assert.Equal(t, "beta", whole.LastFailure.Server)

	agentRec := scopeGet(t, f.srv, "/api/v1/work-sessions", f.token)
	var agent contracts.WorkSessionListResponse
	wsData(t, agentRec, &agent)
	require.Equal(t, 1, agent.Total, "only its own session, and only one with something in reach")
	mine := agent.Sessions[0]
	assert.Equal(t, "ws-mine", mine.ID)
	assert.Equal(t, 2, mine.Attempts, "only the alpha part")
	assert.Equal(t, 1, mine.Failures)
	assert.Equal(t, f.base.Truncate(time.Nanosecond), mine.StartedAt.UTC())
	assert.Equal(t, f.base.Add(time.Minute), mine.LastActivityAt.UTC(), "beta's and the builtin's later activity is not shown")
	require.NotNil(t, mine.LastFailure)
	assert.Equal(t, "alpha", mine.LastFailure.Server)
	assert.Equal(t, "connection reset", mine.LastFailure.Reason)
	assert.Empty(t, mine.Profile, "the profile is not part of a scoped view")
	assert.Zero(t, mine.ConnectionCount)
	assert.NotContains(t, agentRec.Body.String(), "beta")
	assert.NotContains(t, agentRec.Body.String(), wsBetaSecret)
	assert.NotContains(t, agentRec.Body.String(), f.prefix, "the token prefix never leaves")

	// One session: the same projection.
	var one contracts.WorkSession
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine", f.token), &one)
	assert.Equal(t, mine.Attempts, one.Attempts)
	assert.Equal(t, mine.LastActivityAt, one.LastActivityAt)
}

// A session that is someone else's, one that has nothing in reach, and one
// that does not exist answer identically, on both id doors.
func TestWorkSessions_ForeignHiddenAndMissingAreTheSameNotFound(t *testing.T) {
	f := newWSFixture(t)
	for _, suffix := range []string{"", "/timeline"} {
		missing := scopeGet(t, f.srv, "/api/v1/work-sessions/ws-nope"+suffix, f.token)
		require.Equal(t, http.StatusNotFound, missing.Code)
		for _, id := range []string{"ws-other", "ws-hidden"} {
			rec := scopeGet(t, f.srv, "/api/v1/work-sessions/"+id+suffix, f.token)
			assert.Equal(t, http.StatusNotFound, rec.Code, id+suffix)
			assert.Equal(t, wsNormalize(missing), wsNormalize(rec), "%s%s must read like a missing id", id, suffix)
		}
		// Precondition: these really exist for an administrator.
		assert.Equal(t, http.StatusOK, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-other"+suffix, scopeAdminAPIKey).Code)
		assert.Equal(t, http.StatusOK, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-hidden"+suffix, scopeAdminAPIKey).Code)
	}
}

// FR-048: activity on a hidden server changes nothing a scoped caller reads.
func TestWorkSessions_HiddenActivityChangesNothingInReach(t *testing.T) {
	f := newWSFixture(t)
	read := func() (string, string, string) {
		return wsNormalize(scopeGet(t, f.srv, "/api/v1/work-sessions?sort=last_failure", f.token)),
			wsNormalize(scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine", f.token)),
			wsNormalize(scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline?at=end", f.token))
	}
	l1, s1, t1 := read()

	f.row("ws-mine", "scoped-ci", f.prefix, 5*time.Minute, func(r *storage.ActivityRecord) {
		r.ServerName, r.ToolName, r.Status, r.ErrorMessage = "beta", "beta_tool", storage.ActivityStatusError, wsBetaSecret
	})
	f.event("beta", 5*time.Minute, "")

	l2, s2, t2 := read()
	assert.Equal(t, l1, l2)
	assert.Equal(t, s1, s2)
	assert.Equal(t, t1, t2)
}

// FR-042..FR-044, FR-023, FR-025: the timeline of a scoped caller.
func TestWorkSessionTimeline_ScopedToReach(t *testing.T) {
	f := newWSFixture(t)
	rec := scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline", f.token)
	var tl contracts.WorkSessionTimelineResponse
	wsData(t, rec, &tl)

	// The owned alpha event (another user's credential) is withheld: of the
	// two alpha events inside the span only the shared one is here.
	require.Len(t, tl.Rows, 3, "the alpha call, the shared alpha event, the failed alpha call")
	assert.Equal(t, 3, tl.TotalRows)
	assert.Equal(t, contracts.TimelineKindCall, tl.Rows[0].Kind)
	assert.Equal(t, contracts.TimelineKindUpstreamEvent, tl.Rows[1].Kind, "the event sits between the calls around it (SC-005)")
	assert.Equal(t, contracts.TimelineKindCall, tl.Rows[2].Kind)
	assert.True(t, tl.Rows[2].Failed)
	assert.Equal(t, 2, tl.Position, "the default position is the first failure")
	assert.Equal(t, 1, tl.FailureIndex)
	assert.Equal(t, 1, tl.FailureTotal)
	assert.Equal(t, 1, tl.LifetimeFailures)
	assert.False(t, tl.EarlierFailuresNotStored)
	assert.False(t, tl.HasBefore)
	assert.False(t, tl.HasAfter)

	body := rec.Body.String()
	assert.NotContains(t, body, "beta")
	assert.NotContains(t, body, wsBetaSecret)
	assert.NotContains(t, body, "retrieve_tools", "MCPProxy's own tools are not in a scoped activity view")
	for _, row := range tl.Rows {
		assert.Empty(t, row.Activity.Response, "bodies are omitted by default (FR-044)")
		assert.Empty(t, row.Activity.Arguments)
	}
	assert.NotContains(t, body, wsAlphaSecretArg)

	// Bodies on request.
	var withBodies contracts.WorkSessionTimelineResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline?include_bodies=true", f.token), &withBodies)
	assert.Equal(t, "alpha response", withBodies.Rows[2].Activity.Response)
}

// The administrator's timeline: every row and event, and every position form.
func TestWorkSessionTimeline_PositionsForAnAdministrator(t *testing.T) {
	f := newWSFixture(t)
	get := func(query string) contracts.WorkSessionTimelineResponse {
		var tl contracts.WorkSessionTimelineResponse
		wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline"+query, scopeAdminAPIKey), &tl)
		return tl
	}

	all := get("?before=250&after=250")
	// 4 session rows + alpha shared event + alpha owned event + beta event.
	require.Equal(t, 7, all.TotalRows)
	require.Len(t, all.Rows, 7)
	for i := 1; i < len(all.Rows); i++ {
		assert.False(t, all.Rows[i].Activity.Timestamp.Before(all.Rows[i-1].Activity.Timestamp), "rows are in time order")
		assert.Equal(t, i, all.Rows[i].Index)
	}
	assert.Equal(t, 2, all.FailureTotal)
	assert.Equal(t, 1, all.FailureIndex)
	assert.Equal(t, wsFailedAlphaID, all.Rows[all.Position].Activity.ID)

	second := get("?at=failure:2&before=0&after=0")
	require.Len(t, second.Rows, 1)
	assert.Equal(t, wsFailedBetaID, second.Rows[0].Activity.ID)
	assert.Equal(t, 2, second.FailureIndex)
	assert.True(t, second.HasBefore)
	assert.True(t, second.HasAfter)

	byRow := get("?at=" + url.QueryEscape("row:"+wsFailedBetaID) + "&before=1&after=0")
	require.Len(t, byRow.Rows, 2)
	assert.Equal(t, wsFailedBetaID, byRow.Rows[1].Activity.ID)

	end := get("?at=end&before=0&after=0")
	require.Len(t, end.Rows, 1)
	assert.Equal(t, 6, end.Position)
	assert.Equal(t, 2, end.FailureIndex, "both failures are at or before the end")
	assert.False(t, end.HasAfter)

	for _, bad := range []string{"?at=failure:3", "?at=failure:0", "?at=row:nope", "?at=sideways", "?before=251", "?after=-1"} {
		rec := scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline"+bad, scopeAdminAPIKey)
		assert.Equal(t, http.StatusBadRequest, rec.Code, bad)
	}
}

// FR-043: when failures were pruned, positions count only what is stored and
// the response says so with the lifetime count.
func TestWorkSessionTimeline_PrunedFailuresAreReported(t *testing.T) {
	f := newWSFixture(t)
	// The age cap removes every row older than 8.5 minutes: the first alpha
	// call and the failed alpha call's predecessor stay or go by time; here it
	// takes the session's first two rows, including the alpha failure.
	_, err := f.store.RunActivityRetention(storage.RetentionPolicy{
		Now: f.base.Add(10 * time.Minute), MaxAge: 8*time.Minute + 30*time.Second, SessionWindow: 7 * 24 * time.Hour,
	})
	require.NoError(t, err)

	var tl contracts.WorkSessionTimelineResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-mine/timeline", scopeAdminAPIKey), &tl)
	assert.Equal(t, 1, tl.FailureTotal, "only the beta failure is still stored")
	assert.Equal(t, 2, tl.LifetimeFailures)
	assert.True(t, tl.EarlierFailuresNotStored)
	assert.Equal(t, wsFailedBetaID, tl.Rows[len(tl.Rows)-2].Activity.ID)
	require.NotNil(t, tl.Session.TruncatedStartAt)
}

// FR-041: the list's filters and sort.
func TestWorkSessions_ListFiltersAndSort(t *testing.T) {
	f := newWSFixture(t)
	ids := func(query string) []string {
		var resp contracts.WorkSessionListResponse
		wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions"+query, scopeAdminAPIKey), &resp)
		out := make([]string, 0, len(resp.Sessions))
		for _, s := range resp.Sessions {
			out = append(out, s.ID)
		}
		return out
	}

	assert.Equal(t, []string{"ws-hidden", "ws-mine", "ws-other"}, ids(""), "newest activity first")
	assert.Equal(t, []string{"ws-mine"}, ids("?failed=true"))
	assert.Equal(t, []string{"ws-other"}, ids("?token=someone-else"))
	assert.Equal(t, []string{"ws-mine"}, ids("?profile=work-full"))
	assert.Equal(t, []string{"ws-hidden", "ws-mine", "ws-other"}, ids("?run=nightly"))
	assert.Empty(t, ids("?run=other"))
	assert.Empty(t, ids("?error_class=timeout"))
	assert.Equal(t, []string{"ws-mine", "ws-hidden", "ws-other"}, ids("?sort=last_failure"), "sessions with a failure first")
	assert.Equal(t, []string{"ws-mine"}, ids("?limit=1&offset=1"))
	assert.Empty(t, ids("?since="+url.QueryEscape(f.base.Add(time.Hour).Format(time.RFC3339))))

	// A policy block is a failure, and one switch excludes it.
	f.row("ws-blocked", "scoped-ci", f.prefix, 6*time.Minute, func(r *storage.ActivityRecord) {
		r.Type, r.Status = storage.ActivityTypePolicyDecision, "blocked"
	})
	assert.Contains(t, ids("?failed=true"), "ws-blocked")
	assert.NotContains(t, ids("?failed=true&include_blocked=false"), "ws-blocked")
	assert.Contains(t, ids("?failed=true&include_blocked=false"), "ws-mine")

	for _, bad := range []string{"?limit=0", "?limit=101", "?offset=-1", "?sort=sideways", "?since=yesterday"} {
		assert.Equal(t, http.StatusBadRequest, scopeGet(t, f.srv, "/api/v1/work-sessions"+bad, scopeAdminAPIKey).Code, bad)
	}

	// The profile filter is not an oracle for a scoped caller.
	var scoped contracts.WorkSessionListResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions?profile=work-full", f.token), &scoped)
	assert.Zero(t, scoped.Total)
}
