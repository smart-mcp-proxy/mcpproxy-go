package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 099 — describe_tool check mode.
//
// The fixture is the spec-098 preflight fixture (real storage, real Bleve
// index, INSTRUMENTED upstream so "zero upstream I/O" is a hard count) plus a
// captured activity recorder, because FR-013 makes the record part of the
// answer: a check that cannot be audited is not answered.

type describeCheckFixture struct {
	*preflightFixture
	records   []internalRuntime.PreflightActivity
	recordErr error
}

func newDescribeCheckFixture(t *testing.T, mutate func(cfg *config.Config)) *describeCheckFixture {
	t.Helper()
	fixture := &describeCheckFixture{preflightFixture: newPreflightFixture(t, mutate)}
	fixture.proxy.preflightRecorder = func(rec internalRuntime.PreflightActivity) error {
		fixture.records = append(fixture.records, rec)
		return fixture.recordErr
	}
	return fixture
}

// callCheck invokes describe_tool with the given raw arguments and returns the
// raw result (which may be an error result).
func (f *describeCheckFixture) callCheck(t *testing.T, ctx context.Context, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := f.proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err, "the handler answers with tool-result errors, never transport errors")
	require.NotNil(t, result)
	return result
}

// check runs a successful check-mode call and returns the decoded payload plus
// the raw JSON, so tests can assert on absent keys as well as values.
func (f *describeCheckFixture) check(t *testing.T, ctx context.Context, ids []interface{}, extra map[string]interface{}) (describeCheckPayload, string) {
	t.Helper()
	args := map[string]interface{}{"tool_ids": ids, "check": true}
	for k, v := range extra {
		args[k] = v
	}
	result := f.callCheck(t, ctx, args)
	require.False(t, result.IsError, "check returned an error result: %v", resultText(t, result))
	raw := resultText(t, result)
	var payload describeCheckPayload
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	return payload, raw
}

func checkResultByID(t *testing.T, payload describeCheckPayload, id string) describeCheckResult {
	t.Helper()
	for _, res := range payload.Results {
		if res.ID == id {
			return res
		}
	}
	t.Fatalf("no result for id %q in %+v", id, payload.Results)
	return describeCheckResult{}
}

// seedCheckFixture builds the state every reason cell below is induced from.
func seedCheckFixture(t *testing.T, f *describeCheckFixture) {
	t.Helper()
	f.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	f.addServer(t, &config.ServerConfig{Name: "locked", Enabled: true, Quarantined: true, Protocol: "http"})
	f.addServer(t, &config.ServerConfig{Name: "off", Enabled: false, Protocol: "http"})
	f.addServer(t, &config.ServerConfig{Name: "denied", Enabled: true, Protocol: "http", DisabledTools: []string{"forbidden"}})

	f.indexTool(t, "gh", "create_issue")
	f.indexTool(t, "gh", "pending_tool")
	f.indexTool(t, "gh", "changed_tool")
	f.indexTool(t, "gh", "blocked_tool")
	f.indexTool(t, "locked", "lingering")
	f.indexTool(t, "off", "sleeping")
	f.indexTool(t, "denied", "forbidden")

	for _, approval := range []*storage.ToolApprovalRecord{
		{ServerName: "gh", ToolName: "create_issue", Status: storage.ToolApprovalStatusApproved, CurrentHash: "abc123", HashSchemaVersion: 2},
		{ServerName: "gh", ToolName: "pending_tool", Status: storage.ToolApprovalStatusPending},
		{ServerName: "gh", ToolName: "changed_tool", Status: storage.ToolApprovalStatusChanged},
		{ServerName: "gh", ToolName: "blocked_tool", Status: storage.ToolApprovalStatusApproved, Disabled: true},
	} {
		require.NoError(t, f.storage.SaveToolApproval(approval))
	}
}

// --- FR-004: the payload -----------------------------------------------------

// A ready set answers verdict "ready", one result per id, and NOTHING that
// could pass for a definition — that omission is what makes the 50-id cap safe
// (FR-004/SC-005). It also proves the run is auditable from inside the session:
// the returned request_id is the one on the activity record (SC-006).
func TestDescribeToolCheck_ReadySetPayload(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)
	before := fixture.snapshot(t)

	payload, raw := fixture.check(t, context.Background(), []interface{}{"gh:create_issue"}, nil)

	assert.Equal(t, preflight.VerdictReady, payload.Verdict)
	require.Len(t, payload.Results, 1)
	result := payload.Results[0]
	assert.Equal(t, "gh:create_issue", result.ID)
	assert.Equal(t, preflight.StatusReady, result.Status)
	assert.Empty(t, result.Reason, "ready is a status, not a reason")
	assert.Nil(t, result.Retryable, "a ready result carries no retryable flag")
	assert.Empty(t, result.Action)

	assert.WithinDuration(t, time.Now(), payload.CheckedAt, time.Minute)
	assert.NotEmpty(t, payload.RequestID)

	// No definition fields, and no hash at any tier (FR-004).
	for _, forbidden := range []string{"inputSchema", "call_with", "definitions", "errors", "hash", "annotations"} {
		assert.NotContains(t, raw, forbidden, "check payload must not carry %q", forbidden)
	}

	// FR-010 / SC-006: zero upstream calls, zero mutation.
	assert.Equal(t, int64(0), atomic.LoadInt64(fixture.upstreamHits))
	assert.Equal(t, before, fixture.snapshot(t))

	require.Len(t, fixture.records, 1, "exactly one activity record per check run")
	record := fixture.records[0]
	assert.Equal(t, payload.RequestID, record.RequestID,
		"the agent must be able to hand a human the id that finds the run")
	assert.Equal(t, storage.PreflightSurfaceMCPCheck, record.Surface)
	assert.Equal(t, storage.ActivitySourceMCP, record.Source)
	assert.Equal(t, preflight.VerdictReady, record.Verdict)
	require.Len(t, record.Tools, 1)
	assert.Equal(t, "gh:create_issue", record.Tools[0].ID)
	assert.Equal(t, preflight.StatusReady, record.Tools[0].Status)
	assert.Empty(t, record.Tools[0].Reason)
}

// FR-003: the in-band surface names the same reasons as every other preflight
// surface, with the same retryable/action/verdict, for every cell this fixture
// can induce without a connection-state snapshot.
func TestDescribeToolCheck_ReasonCells(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	cases := []struct {
		name    string
		id      string
		reason  string
		verdict string
	}{
		{"server_quarantined", "locked:lingering", preflight.ReasonServerQuarantined, preflight.VerdictBlocked},
		{"server_disabled", "off:sleeping", preflight.ReasonServerDisabled, preflight.VerdictBlocked},
		{"tool_pending_approval", "gh:pending_tool", preflight.ReasonToolPendingApproval, preflight.VerdictBlocked},
		{"tool_changed", "gh:changed_tool", preflight.ReasonToolChanged, preflight.VerdictBlocked},
		{"tool_blocked_by_user", "gh:blocked_tool", preflight.ReasonToolBlockedByUser, preflight.VerdictBlocked},
		{"tool_denied_by_config", "denied:forbidden", preflight.ReasonToolDeniedByConfig, preflight.VerdictBlocked},
		{"not_found", "gh:no_such_tool", preflight.ReasonNotFound, preflight.VerdictUnknownIDs},
		{"malformed id", "not-an-id", preflight.ReasonNotFound, preflight.VerdictUnknownIDs},
		{"unconfigured server", "nosuch:tool", preflight.ReasonNotFound, preflight.VerdictUnknownIDs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := fixture.check(t, context.Background(), []interface{}{tc.id}, nil)

			require.Len(t, payload.Results, 1)
			result := payload.Results[0]
			assert.Equal(t, preflight.StatusUnavailable, result.Status)
			assert.Equal(t, tc.reason, result.Reason)
			require.NotNil(t, result.Retryable)
			assert.Equal(t, preflight.Retryable(tc.reason), *result.Retryable)
			assert.Equal(t, preflight.DefaultAction(tc.reason), result.Action)
			assert.Equal(t, preflight.DefaultRemediation(tc.reason), result.Remediation)
			assert.NotEmpty(t, result.Detail)
			assert.Equal(t, tc.verdict, payload.Verdict)
		})
	}
}

// FR-009/SC-007: an unconfigured server and an out-of-scope one are the SAME
// answer as an unknown id, field for field — and no auth context can change
// that, including the full admin context the MCP middleware injects for
// unauthenticated /mcp requests.
func TestDescribeToolCheck_ScopeSilenceIsByteIdentical(t *testing.T) {
	fixture := newDescribeCheckFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{{Name: "ops", Servers: []string{"gh"}}}
	})
	seedCheckFixture(t, fixture)
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "secret", "exfiltrate")

	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "scoped-bot",
		AllowedServers: []string{"gh"},
		Permissions:    []string{auth.PermRead},
	})

	payload, _ := fixture.check(t, agentCtx, []interface{}{
		"secret:exfiltrate", // exists, out of scope
		"nosuch:tool",       // server not configured at all
		"gh:no_such_tool",   // configured, in scope, absent
	}, nil)

	outOfScope := checkResultByID(t, payload, "secret:exfiltrate")
	unconfigured := checkResultByID(t, payload, "nosuch:tool")
	absent := checkResultByID(t, payload, "gh:no_such_tool")

	// Compare every field except the id itself.
	normalize := func(res describeCheckResult) describeCheckResult {
		res.ID = ""
		res.DidYouMean = nil // suggestions differ by prefix distance, never by existence
		return res
	}
	assert.Equal(t, normalize(absent), normalize(outOfScope),
		"an out-of-scope id must be indistinguishable from one that does not exist")
	assert.Equal(t, normalize(absent), normalize(unconfigured),
		"an unconfigured server must be indistinguishable from an unknown id")
	assert.Equal(t, preflight.ReasonNotFound, outOfScope.Reason)

	for _, res := range payload.Results {
		for _, suggestion := range res.DidYouMean {
			assert.NotContains(t, suggestion, "secret:", "did_you_mean must never cross the scope boundary")
		}
	}

	// A full ADMIN session — what the MCP middleware injects for every
	// unauthenticated /mcp request — narrowed by a path-pinned profile gets the
	// same scope-silence. At the operator tier this id would report
	// server_not_in_scope and name the profile; in band it never can, because
	// the tier is pinned and IsAdmin() is not consulted for disclosure (FR-009).
	adminCtx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	adminCtx = profile.WithProfileScope(adminCtx, profile.NewProfileScope("ops", []string{"gh"}))
	adminPayload, adminRaw := fixture.check(t, adminCtx, []interface{}{"secret:exfiltrate"}, nil)
	adminResult := adminPayload.Results[0]
	assert.Equal(t, preflight.ReasonNotFound, adminResult.Reason,
		"an admin MCP session gets the agent-token tier like every other in-band caller")
	assert.NotContains(t, adminRaw, "ops", "the scope's name is operator-tier disclosure")

	// The REST surface over the same state and the same profile DOES name it —
	// that is where an operator goes for the full diagnosis.
	restOutcome, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools:   []preflight.ToolRef{{ID: "secret:exfiltrate"}},
		Profile: "ops",
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonServerNotInScope, restOutcome.Results[0].Reason)
}

// FR-004: a ready result never carries a hash, even for a caller whose REST
// equivalent would see one.
func TestDescribeToolCheck_NeverDisclosesAHash(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	_, raw := fixture.check(t, context.Background(), []interface{}{"gh:create_issue"}, nil)
	assert.NotContains(t, raw, "abc123", "the stored hash must never reach an in-band caller")

	// The REST path over the same state DOES disclose it at the operator tier —
	// the divergence is deliberate, not an accident of this fixture.
	outcome, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "sha256/v2:abc123", outcome.Results[0].Hash)
}

// FR-004: a misspelled id gets a scope-filtered did_you_mean, and one bad id
// never fails the batch (US1 acceptance 4).
func TestDescribeToolCheck_DidYouMeanAndBatchResilience(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	payload, _ := fixture.check(t, context.Background(), []interface{}{
		"gh:create_isue", "gh:create_issue", "not-an-id",
	}, nil)

	require.Len(t, payload.Results, 3)
	misspelled := checkResultByID(t, payload, "gh:create_isue")
	assert.Equal(t, preflight.ReasonNotFound, misspelled.Reason)
	assert.Contains(t, misspelled.DidYouMean, "gh:create_issue")
	assert.LessOrEqual(t, len(misspelled.DidYouMean), 3)

	assert.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "gh:create_issue").Status)
	assert.Equal(t, preflight.ReasonNotFound, checkResultByID(t, payload, "not-an-id").Reason)
	assert.Equal(t, preflight.VerdictUnknownIDs, payload.Verdict)
}

// --- FR-005/FR-006: batch shape ---------------------------------------------

// 50 raw ids are evaluated in one call; 51 fails outright and evaluates
// nothing — including writing no activity record, since nothing ran.
func TestDescribeToolCheck_BatchCapBoundary(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	ids := make([]interface{}, 0, maxDescribeCheckIDs+1)
	for i := 0; i < maxDescribeCheckIDs; i++ {
		ids = append(ids, fmt.Sprintf("gh:tool_%02d", i))
	}
	payload, raw := fixture.check(t, context.Background(), ids, nil)
	assert.Len(t, payload.Results, maxDescribeCheckIDs, "all 50 ids are evaluated in one call")
	assert.NotContains(t, raw, "inputSchema")
	require.Len(t, fixture.records, 1)
	assert.Len(t, fixture.records[0].Tools, maxDescribeCheckIDs)

	fixture.records = nil
	result := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": append(ids, "gh:one_too_many"),
		"check":    true,
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "too many tool_ids: 51 (max 50")
	assert.NotContains(t, text, "verdict", "an over-cap call evaluates nothing")
	assert.Empty(t, fixture.records, "a rejected request runs no check and records nothing")
}

// FR-006: ids are trimmed, then deduplicated, one result per unique id in
// first-occurrence order, echoing the NORMALIZED id so a caller can join
// results back to its own list.
func TestDescribeToolCheck_NormalizationAndDedup(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	payload, _ := fixture.check(t, context.Background(), []interface{}{
		" gh:pending_tool ", "gh:create_issue", "gh:pending_tool", "gh:create_issue ",
	}, nil)

	require.Len(t, payload.Results, 2, "duplicates collapse to one result each")
	assert.Equal(t, "gh:pending_tool", payload.Results[0].ID, "first-occurrence order, normalized id")
	assert.Equal(t, "gh:create_issue", payload.Results[1].ID)

	require.Len(t, fixture.records, 1)
	assert.Len(t, fixture.records[0].Tools, 2,
		"the record counts UNIQUE ids, exactly as the REST record does")
}

// --- FR-007: filters ---------------------------------------------------------

// A tool whose upstream definition declares no annotations is withheld by each
// of the three filters, with the reason at its precedence slot. (Explicitly
// unsafe annotations produce policy_filtered; they are only readable from a
// connection-state snapshot, so that half is asserted in the matrix rows.)
func TestDescribeToolCheck_FiltersWithholdUnannotatedTools(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	for _, filter := range describeCheckFilterKeys {
		t.Run(filter, func(t *testing.T) {
			payload, _ := fixture.check(t, context.Background(), []interface{}{"gh:create_issue"},
				map[string]interface{}{"filters": map[string]interface{}{filter: true}})

			result := payload.Results[0]
			assert.Equal(t, preflight.ReasonMissingAnnotation, result.Reason)
			require.NotNil(t, result.Retryable)
			assert.False(t, *result.Retryable)
			assert.Equal(t, preflight.VerdictBlocked, payload.Verdict)
			assert.Contains(t, result.Detail, filter, "the detail names the filter that withheld it")
		})
	}

	// All three false is the same as no filter at all.
	payload, _ := fixture.check(t, context.Background(), []interface{}{"gh:create_issue"},
		map[string]interface{}{"filters": map[string]interface{}{
			"read_only_only": false, "exclude_destructive": false, "exclude_open_world": false,
		}})
	assert.Equal(t, preflight.StatusReady, payload.Results[0].Status)
}

// --- FR-012 / FR-012a: request errors ---------------------------------------

// Every shape the handler cannot honor exactly is a request error naming the
// rule — never a coerced mode and never a verdict. None of these executes a
// check, so none writes a record.
func TestDescribeToolCheck_StrictArgumentValidation(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	ids := []interface{}{"gh:create_issue"}
	cases := []struct {
		name     string
		args     map[string]interface{}
		contains []string
	}{
		{
			name:     "check null is not absent",
			args:     map[string]interface{}{"tool_ids": ids, "check": nil},
			contains: []string{"'check' must be a boolean", "null"},
		},
		{
			name:     "check as a string",
			args:     map[string]interface{}{"tool_ids": ids, "check": "true"},
			contains: []string{"'check' must be a boolean", "string"},
		},
		{
			name:     "check as a number",
			args:     map[string]interface{}{"tool_ids": ids, "check": float64(1)},
			contains: []string{"'check' must be a boolean", "number"},
		},
		{
			name:     "filters without check",
			args:     map[string]interface{}{"tool_ids": ids, "filters": map[string]interface{}{"read_only_only": true}},
			contains: []string{"'filters' requires 'check': true"},
		},
		{
			name:     "filters with check false",
			args:     map[string]interface{}{"tool_ids": ids, "check": false, "filters": map[string]interface{}{"read_only_only": true}},
			contains: []string{"'filters' requires 'check': true"},
		},
		{
			name:     "filters not an object",
			args:     map[string]interface{}{"tool_ids": ids, "check": true, "filters": "read_only_only"},
			contains: []string{"'filters' must be an object", "string"},
		},
		{
			name:     "unknown filter member",
			args:     map[string]interface{}{"tool_ids": ids, "check": true, "filters": map[string]interface{}{"read_only": true}},
			contains: []string{"unknown member 'filters.read_only'", "exclude_open_world"},
		},
		{
			name:     "non-boolean filter value",
			args:     map[string]interface{}{"tool_ids": ids, "check": true, "filters": map[string]interface{}{"read_only_only": "yes"}},
			contains: []string{"'filters.read_only_only' must be a boolean", "string"},
		},
		{
			name:     "reserved expect_hashes with check",
			args:     map[string]interface{}{"tool_ids": ids, "check": true, "expect_hashes": map[string]interface{}{"gh:create_issue": "sha256/v2:abc123"}},
			contains: []string{"'expect_hashes' is reserved", "preflight"},
		},
		{
			name:     "reserved expect_hashes without check",
			args:     map[string]interface{}{"tool_ids": ids, "expect_hashes": map[string]interface{}{}},
			contains: []string{"'expect_hashes' is reserved"},
		},
		{
			name:     "empty ids under check",
			args:     map[string]interface{}{"tool_ids": []interface{}{}, "check": true},
			contains: []string{"tool_ids", "1-50"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.records = nil
			result := fixture.callCheck(t, context.Background(), tc.args)
			require.True(t, result.IsError, "this shape must be rejected")
			text := resultText(t, result)
			for _, want := range tc.contains {
				assert.Contains(t, text, want)
			}
			assert.NotContains(t, text, "verdict", "a rejected request evaluates nothing")
			assert.Empty(t, fixture.records, "a rejected request writes no activity record")
		})
	}
}

// The check-mode empty-ids error must not reuse the plain-mode wording, which
// names a cap ten times smaller (FR-012).
func TestDescribeToolCheck_EmptyIDsErrorIsCheckModeAccurate(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)

	result := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{}, "check": true,
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "1-50 tool ids")
	assert.NotContains(t, text, "1-5 tool ids")
}

// FR-012: a runtime that cannot evaluate honestly produces a tool ERROR saying
// no verdict was computed — never a fabricated "everything is fine".
func TestDescribeToolCheck_RuntimeUnavailableIsAnError(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	savedIndex := fixture.proxy.index
	fixture.proxy.index = nil
	t.Cleanup(func() { fixture.proxy.index = savedIndex })

	result := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue"}, "check": true,
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "no verdict was computed")
	assert.NotContains(t, text, "\"verdict\"", "a refusal is never a verdict")
	assert.Empty(t, fixture.records)
}

// FR-013: a preflight nobody can audit is not answered. The write happens
// BEFORE the verdict is returned, so its failure fails the call.
func TestDescribeToolCheck_ActivityWriteFailureFailsTheCall(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)
	fixture.recordErr = internalRuntime.ErrActivityUnavailable

	result := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue"}, "check": true,
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "activity record could not be persisted")
	assert.NotContains(t, resultText(t, result), "\"verdict\"")
	require.Len(t, fixture.records, 1, "the write was attempted, and its failure was fatal")
}

// A proxy with no activity service at all cannot audit a check either, so it
// refuses rather than answering unauditably.
func TestDescribeToolCheck_NoActivityServiceRefuses(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)
	fixture.proxy.preflightRecorder = nil // no recorder, and no runtime behind it

	result := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue"}, "check": true,
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "activity record could not be persisted")
}

// --- FR-011: plain mode is untouched ----------------------------------------

// check:false and an absent check are the same request: definitions, the 5-id
// cap with its original wording, and duplicates rendered once per occurrence.
func TestDescribeToolCheck_PlainModeUnaffected(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)

	absent := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue"},
	})
	explicit := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue"}, "check": false,
	})
	require.False(t, absent.IsError)
	assert.Equal(t, resultText(t, absent), resultText(t, explicit),
		"check:false is byte-identical to omitting the parameter")
	assert.Contains(t, resultText(t, absent), "definitions")
	assert.NotContains(t, resultText(t, absent), "verdict")
	assert.Empty(t, fixture.records, "plain mode writes no preflight record")

	// The 50-id cap does not leak into plain mode, and the over-cap wording is
	// the pre-099 one verbatim.
	ids := make([]interface{}, 0, maxDescribeToolIDs+1)
	for i := 0; i <= maxDescribeToolIDs; i++ {
		ids = append(ids, fmt.Sprintf("gh:tool_%02d", i))
	}
	overCap := fixture.callCheck(t, context.Background(), map[string]interface{}{"tool_ids": ids})
	require.True(t, overCap.IsError)
	assert.Equal(t, "too many tool_ids: 6 (max 5). Narrow your selection.", resultText(t, overCap))

	// Duplicates: plain mode renders one entry per occurrence (dedup is
	// check-mode only).
	dupes := fixture.callCheck(t, context.Background(), map[string]interface{}{
		"tool_ids": []interface{}{"gh:create_issue", "gh:create_issue"},
	})
	require.False(t, dupes.IsError)
	assert.Equal(t, 2, strings.Count(resultText(t, dupes), `"name":"gh:create_issue"`))
}

// --- FR-004 projection over state-injected results --------------------------

// The connection-state and explicitly-unsafe-annotation cells cannot be induced
// without a live stateview, exactly as spec 098 found for its pending_auth cell.
// What is in-band-specific about them is the PROJECTION — which fields the MCP
// payload keeps, drops and renames — so that is what this asserts, over
// evaluator results produced by the same evaluator the handler calls.
func TestDescribeToolCheck_PayloadProjectionIsLossless(t *testing.T) {
	results := []preflight.Result{
		{
			ID: "oauthy:sync", Status: preflight.StatusUnavailable, Reason: preflight.ReasonOAuthRequired,
			Retryable: false, Action: "login", Detail: "d", Remediation: "r",
		},
		{
			ID: "slow:boot", Status: preflight.StatusUnavailable, Reason: preflight.ReasonServerInitializing,
			Retryable: true, Detail: "d", Remediation: "r",
		},
		{
			// A ready result WITH a hash: the projection must drop it.
			ID: "gh:create_issue", Status: preflight.StatusReady, Hash: "sha256/v2:abc123",
		},
	}
	outcome := preflight.Outcome{Verdict: preflight.VerdictForResults(results), Results: results}

	payload := describeCheckResponse(outcome, "req-1", time.Now().UTC())
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	assert.Equal(t, preflight.VerdictBlocked, payload.Verdict)
	assert.NotContains(t, string(raw), "abc123", "no hash is ever returned in band (FR-004)")

	oauth := checkResultByID(t, payload, "oauthy:sync")
	require.NotNil(t, oauth.Retryable)
	assert.False(t, *oauth.Retryable)
	assert.Equal(t, "login", oauth.Action)

	initializing := checkResultByID(t, payload, "slow:boot")
	require.NotNil(t, initializing.Retryable)
	assert.True(t, *initializing.Retryable, "an initializing server tells the agent to wait, not to escalate")
	assert.Empty(t, initializing.Action, "a reason with no action emits no action key")
}
