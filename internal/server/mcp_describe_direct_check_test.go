package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 102 T051–T055: describe_tool `check: true` on the DIRECT surface.
//
// The evaluator accepts colon ids only, so without a canonicalize-and-restore
// adapter every `server__tool` id an agent copied out of its own tools/list
// would answer not_found — check mode would be unusable on exactly the surface
// that most needs it.

type directCheckFixture struct {
	proxy   *MCPProxyServer
	records []internalRuntime.PreflightActivity
}

func newDirectCheckFixture(t *testing.T) *directCheckFixture {
	t.Helper()
	f := &directCheckFixture{proxy: newDirectDescribeProxy(t)}
	f.proxy.preflightRecorder = func(rec internalRuntime.PreflightActivity) error {
		f.records = append(f.records, rec)
		return nil
	}
	return f
}

func (f *directCheckFixture) check(t *testing.T, ctx context.Context, ids []interface{}) describeCheckPayload {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": ids, "check": true}
	result, err := f.proxy.describeToolHandler(describeSurfaceDirect)(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "check returned an error result: %v", result.Content)

	var payload describeCheckPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload))
	return payload
}

func checkResultsByID(payload describeCheckPayload) map[string]describeCheckResult {
	byID := make(map[string]describeCheckResult, len(payload.Results))
	for _, r := range payload.Results {
		byID[r.ID] = r
	}
	return byID
}

// T051: a direct id gets its verdict back under the id the CALLER sent, in the
// order they were sent.
func TestDescribeDirectCheck_AnswersUnderTheCallersOwnIDs(t *testing.T) {
	f := newDirectCheckFixture(t)

	payload := f.check(t, context.Background(), []interface{}{
		"we__ird__do_thing", "github__read_file", "github:create_issue",
	})

	require.Len(t, payload.Results, 3)
	assert.Equal(t, []string{"we__ird__do_thing", "github__read_file", "github:create_issue"},
		[]string{payload.Results[0].ID, payload.Results[1].ID, payload.Results[2].ID},
		"ids and their order must be the caller's, not the evaluator's canonical form")

	for _, r := range payload.Results {
		assert.Equalf(t, string(preflight.StatusReady), r.Status, "id %s should be ready", r.ID)
	}
	assert.Equal(t, string(preflight.VerdictReady), payload.Verdict)
}

// T044 (check half): a listed-but-pending tool reports the evaluator's REAL
// reason, projected through untouched — not a flattened not_found.
func TestDescribeDirectCheck_PendingToolReportsItsRealReason(t *testing.T) {
	f := newDirectCheckFixture(t)
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "read_file",
		Status:     storage.ToolApprovalStatusPending,
	}))

	payload := f.check(t, context.Background(), []interface{}{"github__read_file"})

	require.Len(t, payload.Results, 1)
	result := payload.Results[0]
	assert.Equal(t, "github__read_file", result.ID)
	assert.Equal(t, string(preflight.StatusUnavailable), result.Status)
	assert.Equal(t, string(preflight.ReasonToolPendingApproval), result.Reason,
		"the adapter projects the evaluator's reason, it does not translate it")
	require.NotNil(t, result.Retryable)
}

// An id this session may not see is gated BEFORE the evaluator and answers a
// plain not_found — byte-indistinguishable from an id that does not exist.
func TestDescribeDirectCheck_InvisibleIDsGatedBeforeTheEvaluator(t *testing.T) {
	f := newDirectCheckFixture(t)

	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	payload := f.check(t, readOnly, []interface{}{"github__delete_repo", "github__nope"})
	byID := checkResultsByID(payload)

	require.Contains(t, byID, "github__delete_repo")
	require.Contains(t, byID, "github__nope")

	gated := byID["github__delete_repo"]
	absent := byID["github__nope"]
	assert.Equal(t, string(preflight.StatusUnavailable), gated.Status)
	assert.Equal(t, string(preflight.ReasonNotFound), gated.Reason)

	// Byte-for-byte the same answer, minus the id: a tier-invisible tool must
	// not be distinguishable from one that was never there.
	gated.ID, absent.ID = "", ""
	assert.Equal(t, absent, gated,
		"a gated id and an absent id must produce identical results")
}

// T055: suggestion discipline. A read-scoped token's miss must not be handed a
// did_you_mean naming a tool absent from its own listing.
func TestDescribeDirectCheck_SuggestionsNeverCrossTheVisibilityBoundary(t *testing.T) {
	f := newDirectCheckFixture(t)

	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	// A near-miss of the DESTRUCTIVE tool this token cannot list.
	payload := f.check(t, readOnly, []interface{}{"github__delete_rep"})
	require.Len(t, payload.Results, 1)

	for _, suggestion := range payload.Results[0].DidYouMean {
		assert.NotContains(t, suggestion, "delete_repo",
			"a suggestion must never name a tool the caller cannot list")
	}

	// Not vacuous: the SAME token, missing a tool it CAN list, is suggested it.
	// Without this the assertion above would also pass on a build that never
	// suggests anything.
	visible := f.check(t, readOnly, []interface{}{"github__read_fil"})
	require.Len(t, visible.Results, 1)
	assert.Contains(t, visible.Results[0].DidYouMean, "github__read_file",
		"suggestions must work for this token, or the discipline assertion above proves nothing")
}

// Suggestions on this surface are drawn from the CATALOG, not the search index:
// the fixture tools are never indexed, so an index-backed corpus would suggest
// nothing at all.
func TestDescribeDirectCheck_SuggestionsComeFromTheCatalog(t *testing.T) {
	f := newDirectCheckFixture(t)
	require.Nil(t, f.proxy.lookupIndexedTool("github", "read_file"),
		"the fixture must NOT be indexed, or this asserts nothing")

	payload := f.check(t, context.Background(), []interface{}{"github:read_fil"})
	require.Len(t, payload.Results, 1)
	assert.Contains(t, payload.Results[0].DidYouMean, "github:read_file",
		"did_you_mean must be drawn from the catalog corpus on this surface")
}

// The activity record carries the ids the caller actually sent (FR-013): an
// operator correlating a complaint has only the agent's own ids to search by.
func TestDescribeDirectCheck_ActivityRecordKeepsCallerIDs(t *testing.T) {
	f := newDirectCheckFixture(t)

	f.check(t, context.Background(), []interface{}{"github__read_file"})

	require.Len(t, f.records, 1)
	record := f.records[0]
	require.NotNil(t, record.Arguments)
	assert.Equal(t, []string{"github__read_file"}, record.Arguments.ToolIDs)
	require.Len(t, record.Tools, 1)
	assert.Equal(t, "github__read_file", record.Tools[0].ID,
		"the per-tool outcome must be recorded under the caller's id")
}

// The indexed surface is untouched: a direct-form id there is still malformed.
func TestDescribeCheck_IndexedSurfaceStillRejectsDirectIDs(t *testing.T) {
	f := newDirectCheckFixture(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"tool_ids": []interface{}{"github__read_file"}, "check": true,
	}
	result, err := f.proxy.handleDescribeTool(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	var payload describeCheckPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload))
	require.Len(t, payload.Results, 1)
	assert.Equal(t, string(preflight.StatusUnavailable), payload.Results[0].Status)
	assert.Equal(t, string(preflight.ReasonNotFound), payload.Results[0].Reason)
}
