package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Release QA F2 (SC-005): a session that starts while a server it then fails
// on is already down shows the event that explains it, from before the
// session, marked as such; a server it called without failing adds nothing,
// and a scoped caller sees only events of servers in its reach.
func TestWorkSessionTimeline_OpenEventBeforeTheSession(t *testing.T) {
	f := newWSFixture(t)
	start := f.base.Add(10 * time.Minute)
	f.row("ws-late", "scoped-ci", f.prefix, 10*time.Minute, func(r *storage.ActivityRecord) {
		r.Status, r.ErrorClass, r.ErrorMessage = storage.ActivityStatusError, "network", "not connected"
	})
	f.row("ws-late", "scoped-ci", f.prefix, 11*time.Minute, func(r *storage.ActivityRecord) { r.ServerName, r.ToolName = "beta", "beta_tool" })
	f.event("alpha", 9*time.Minute, "") // alpha went down a minute before
	f.event("beta", 9*time.Minute, "")  // beta too, but the session's beta call worked

	var tl contracts.WorkSessionTimelineResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-late/timeline?before=250&after=250", scopeAdminAPIKey), &tl)
	require.GreaterOrEqual(t, len(tl.Rows), 3)
	first := tl.Rows[0]
	assert.Equal(t, contracts.TimelineKindUpstreamEvent, first.Kind)
	assert.True(t, first.PrecedesSession)
	require.NotNil(t, first.Activity)
	assert.Equal(t, "alpha", first.Activity.ServerName)
	assert.True(t, first.At.Before(start))
	assert.Equal(t, contracts.TimelineKindSessionStart, tl.Rows[1].Kind)
	for _, r := range tl.Rows[1:] {
		assert.False(t, r.PrecedesSession)
		if r.Activity != nil && r.Kind == contracts.TimelineKindUpstreamEvent {
			assert.NotEqual(t, "beta", r.Activity.ServerName, "no failure on beta: nothing to explain")
		}
	}
	assert.Equal(t, 1, tl.FailureTotal)
	assert.Equal(t, contracts.TimelineKindCall, tl.Rows[tl.Position].Kind, "still positioned at the failure")
	assert.Equal(t, len(tl.Rows), tl.TotalRows)

	// The scoped token reaches alpha only; its view has the alpha event too.
	var scoped contracts.WorkSessionTimelineResponse
	wsData(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-late/timeline", f.token), &scoped)
	require.NotEmpty(t, scoped.Rows)
	assert.True(t, scoped.Rows[0].PrecedesSession)
	assert.NotContains(t, scopeGet(t, f.srv, "/api/v1/work-sessions/ws-late/timeline", f.token).Body.String(), `"beta"`)
}
