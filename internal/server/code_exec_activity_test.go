package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// jsruntime.Execute takes its options BY VALUE and only fills a missing
// ExecutionID on its own copy, so leaving it unset in the handler left every
// consumer of options.ExecutionID holding "". The parent history record is the
// visible symptom: it is keyed by the correlation id it was minted with, and
// its RequestID — the handle `activity list --request-id` takes — was empty, so
// nothing could be correlated back to the execution that produced it.
func TestCodeExecution_ParentRecordCarriesItsCorrelationID(t *testing.T) {
	proxy, _ := newStoredScriptProxy(t)

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "code_execution",
		Arguments: map[string]interface{}{
			"code":    `1 + 1`,
			"input":   map[string]interface{}{},
			"options": map[string]interface{}{"timeout_ms": 10000},
		},
	}}
	result, err := proxy.handleCodeExecution(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)

	records, err := proxy.storage.GetServerToolCalls("code_execution", 10)
	require.NoError(t, err)
	require.Len(t, records, 1)

	rec := records[0]
	assert.NotEmpty(t, rec.RequestID, "the execution id must reach the record")
	assert.Equal(t, rec.ID, rec.RequestID,
		"the parent call id and its correlation handle are the same value")
}

// A nested call must be linkable to the execution that issued it. The link is
// the parent's correlation id, carried on the nested record as both RequestID
// (the shared correlation handle) and ParentCallID.
func TestCodeExecution_NestedHistoryLinksToTheParentExecution(t *testing.T) {
	proxy, _ := newStoredScriptProxy(t)

	serverCfg := &config.ServerConfig{
		Name: "time", URL: "http://localhost:1/mcp", Protocol: "streamable-http", Enabled: true,
	}
	require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "code_execution",
		Arguments: map[string]interface{}{
			// The server is configured but has no client, so the call fails
			// inside the sandbox — which is exactly a path that must still be
			// recorded and still be linked to its parent.
			"code":    `var r = call_tool("time", "get_current_time", {}); ({ ok: r.ok })`,
			"input":   map[string]interface{}{},
			"options": map[string]interface{}{"timeout_ms": 10000},
		},
	}}
	_, err := proxy.handleCodeExecution(context.Background(), request)
	require.NoError(t, err)

	parents, err := proxy.storage.GetServerToolCalls("code_execution", 10)
	require.NoError(t, err)
	require.Len(t, parents, 1)
	parentID := parents[0].ID

	nested, err := proxy.storage.GetServerToolCalls(storage.GenerateServerID(serverCfg), 10)
	require.NoError(t, err)
	require.Len(t, nested, 1, "the sandboxed call must be recorded")

	assert.Equal(t, parentID, nested[0].ParentCallID)
	assert.Equal(t, parentID, nested[0].RequestID,
		"the nested record shares the parent's correlation handle")
	assert.NotEqual(t, parentID, nested[0].ID, "but it is its own record")
}

// The status recorded for a sandboxed sub-call is DERIVED, never assumed. An
// upstream that answered isError:true failed even though the transport hop
// succeeded, and a Go error wins over the upstream's own words.
func TestSubCallActivityOutcome_ClassifiesEveryExit(t *testing.T) {
	t.Run("normal answer", func(t *testing.T) {
		status, errMsg, response, truncated := subCallActivityOutcome(mcp.NewToolResultText("42"), nil)
		assert.Equal(t, storage.ActivityStatusSuccess, status)
		assert.Empty(t, errMsg)
		assert.Contains(t, response, "42")
		assert.False(t, truncated)
	})

	t.Run("upstream answered isError", func(t *testing.T) {
		status, errMsg, _, _ := subCallActivityOutcome(mcp.NewToolResultError("Invalid timezone"), nil)
		assert.Equal(t, storage.ActivityStatusError, status)
		assert.Contains(t, errMsg, "Invalid timezone")
	})

	t.Run("policy refusal / transport error", func(t *testing.T) {
		status, errMsg, response, truncated := subCallActivityOutcome(nil, errors.New("server \"evil\" is quarantined"))
		assert.Equal(t, storage.ActivityStatusError, status)
		assert.Contains(t, errMsg, "quarantined")
		assert.Empty(t, response, "a call that never happened has no response")
		assert.False(t, truncated)
	})

	t.Run("oversized answers are capped", func(t *testing.T) {
		_, _, response, truncated := subCallActivityOutcome(
			mcp.NewToolResultText(strings.Repeat("é", subCallActivityResponseLimit)), nil)
		assert.True(t, truncated, "one script can issue many calls; each record is capped")
		assert.LessOrEqual(t, len(response), subCallActivityResponseLimit)
		assert.True(t, utf8.ValidString(response), "truncation must not split a rune")
	})
}

// emitSubCallActivity runs on a caller that unit tests drive without a proxy at
// all. It must stay a no-op there rather than panicking.
func TestEmitSubCallActivity_NoProxyIsANoOp(t *testing.T) {
	u := &upstreamToolCaller{logger: zap.NewNop()}
	assert.NotPanics(t, func() {
		u.emitSubCallActivity("time", "now", nil, nil, errors.New("boom"), time.Now(), time.Millisecond)
	})
}
