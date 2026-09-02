package server

import (
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Every emitter that can set response_truncated must state which copies its cut
// shortened, and these tests assert the value on the wire, from the real emit
// path.
//
// The one that matters most is the code-execution sub-call. It is a
// Type=tool_call record whose cut is a LOG-side cut at
// subCallActivityResponseLimit, so it points the opposite way from every other
// tool_call — which is what defeated round 4's per-type resolver. Nothing infers
// the direction any more, but a stamp set to the wrong constant would recreate
// the same wrong sentence, so the value is pinned here at the emitter.

// awaitToolCallEvent drains the bus until a tool-call completion arrives.
func awaitToolCallEvent(t *testing.T, ch <-chan runtime.Event, want runtime.EventType) runtime.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == want {
				return evt
			}
		case <-deadline:
			t.Fatalf("no %s event within 5s", want)
		}
	}
}

// A sandbox sub-call whose recorded text was cut at the 8KB activity-log limit.
// The script received the WHOLE result; only the log copy is short.
func TestSubCallEmitterStampsRecordOnly(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	caller := &upstreamToolCaller{proxy: proxy, sessionID: "sess-1", parentCallID: "parent-1"}
	oversized := mcp.NewToolResultText(strings.Repeat("x", subCallActivityResponseLimit*2))

	// Premise: the fixture really does trip the log-side cut. Without this the
	// assertion below would pass on a result that was never truncated.
	_, _, _, truncated := subCallActivityOutcome(oversized, nil)
	require.True(t, truncated, "fixture must exceed subCallActivityResponseLimit")

	caller.emitSubCallActivity("github", "search", nil, oversized, nil,
		time.Now(), 5*time.Millisecond)

	evt := awaitToolCallEvent(t, ch, runtime.EventTypeActivityToolCallCompleted)
	assert.Equal(t, true, evt.Payload["response_truncated"])
	assert.Equal(t, string(contracts.CutShortenedRecordOnly), evt.Payload["response_truncation_cut"],
		"the sandbox got the whole result; only the activity log was cut. "+
			"CutShortenedAgentAndRecord here would tell a reader the record IS the "+
			"delivered copy, which is the round-4 defect")

	// And the cut it describes is NOT tool_response_limit, so the resolved
	// sentence must not send an operator to that setting.
	notice := contracts.ResolveResponseTruncation(
		contracts.CutShortenedRecordOnly, true, false).Notice
	assert.NotContains(t, notice, "tool_response_limit")
}

// The same emitter, untruncated: a blanket stamp would be as wrong as a blanket
// direction.
func TestSubCallEmitterStampsNothingWhenNothingWasCut(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	caller := &upstreamToolCaller{proxy: proxy, sessionID: "sess-1", parentCallID: "parent-1"}
	caller.emitSubCallActivity("github", "search", nil, mcp.NewToolResultText("small"), nil,
		time.Now(), time.Millisecond)

	evt := awaitToolCallEvent(t, ch, runtime.EventTypeActivityToolCallCompleted)
	assert.Equal(t, false, evt.Payload["response_truncated"])
	assert.Equal(t, string(contracts.CutNone), evt.Payload["response_truncation_cut"])
}

// A refused sub-call never produced a response at all, so there is nothing for
// a cut to have shortened.
func TestRefusedSubCallStampsNothing(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	caller := &upstreamToolCaller{proxy: proxy, sessionID: "sess-1", parentCallID: "parent-1"}
	caller.emitSubCallRefused("github", "search", nil,
		assertQuarantined(), time.Now(), time.Millisecond)

	evt := awaitToolCallEvent(t, ch, runtime.EventTypeActivityToolCallCompleted)
	assert.Equal(t, false, evt.Payload["response_truncated"])
	assert.Equal(t, string(contracts.CutNone), evt.Payload["response_truncation_cut"])
	// The one live path that still records a true zero for response bytes.
	assert.Nil(t, evt.Payload["response_bytes"],
		"a refused call produced no response; zero here is TRUE, not unknown")
}

func assertQuarantined() error {
	return errQuarantinedFixture
}

var errQuarantinedFixture = quarantinedFixtureError{}

type quarantinedFixtureError struct{}

func (quarantinedFixtureError) Error() string {
	return `server "github" is quarantined for security review`
}

// agentOnlyCut is what both BUILT-IN call sites pass, and both must keep
// passing it: their records hold the pre-cut text while the agent got it cut.
func TestAgentOnlyCutHelperMapsBothWays(t *testing.T) {
	assert.Equal(t, contracts.CutShortenedAgentOnly, agentOnlyCut(true))
	assert.Equal(t, contracts.CutNone, agentOnlyCut(false))
}

// The two tool_call populations must resolve to different sentences all the way
// from the emitter's constant, which is the property four review rounds could
// not express at all.
func TestTheTwoToolCallPopulationsResolveDifferently(t *testing.T) {
	forward := contracts.ResolveResponseTruncation(contracts.CutShortenedAgentAndRecord, true, false)
	subCall := contracts.ResolveResponseTruncation(contracts.CutShortenedRecordOnly, true, false)

	require.NotEqual(t, forward.Relation, subCall.Relation)
	require.NotEqual(t, forward.Notice, subCall.Notice)
	assert.Equal(t, contracts.StoredEqualsDelivered, forward.Relation)
	assert.Equal(t, contracts.StoredSmallerThanDelivered, subCall.Relation)
}
