package server

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Review finding — `code_execution` is a FOURTH upstream dispatch path, and it
// was still classifying on `err == nil` alone.
//
// A tool invoked from the JS sandbox goes through upstreamToolCaller.CallTool,
// which recorded `success: err == nil` and only ever set record.Error from a Go
// error. An upstream answering `CallToolResult{IsError:true}` — the most common
// real failure — was therefore stored in tool-call history as a clean success,
// while the identical call through call_tool_read now records the upstream's
// own message (issue #935, mcp.go).

func isErrorResult(text string) *mcp.CallToolResult {
	return mcp.NewToolResultError(text)
}

// The in-memory record is what the code_execution response reports back as the
// list of tool calls the script made.
func TestUpstreamToolCaller_RecordsIsErrorResultAsFailure(t *testing.T) {
	u := &upstreamToolCaller{logger: zap.NewNop()}

	start := time.Now()
	u.recordUpstreamCall("time", "get_current_time", start, time.Millisecond,
		isErrorResult("Invalid timezone: 'Mars/Olympus'"), nil)
	u.recordUpstreamCall("time", "get_current_time", start, time.Millisecond,
		mcp.NewToolResultText("2026-01-01T00:00:00Z"), nil)

	calls := u.getToolCalls()
	require.Len(t, calls, 2)

	assert.False(t, calls[0].Success,
		"an upstream that answered isError:true did not succeed, even though the transport hop did")
	assert.Contains(t, calls[0].Error, "Mars/Olympus",
		"the recorded error must carry the upstream's own explanation")

	assert.True(t, calls[1].Success, "a normal answer must still be a success")
	assert.Empty(t, calls[1].Error)
}

// A transport/dispatch error still wins: its message is the useful one.
func TestUpstreamToolCaller_TransportErrorStillRecorded(t *testing.T) {
	u := &upstreamToolCaller{logger: zap.NewNop()}

	u.recordUpstreamCall("time", "get_current_time", time.Now(), time.Millisecond,
		nil, assert.AnError)

	calls := u.getToolCalls()
	require.Len(t, calls, 1)
	assert.False(t, calls[0].Success)
	assert.Equal(t, assert.AnError.Error(), calls[0].Error)
}

// The persisted history row is what the activity/usage consumers read. Before
// the fix it had an empty Error for an isError answer, so a failed nested call
// was indistinguishable from a successful one.
func TestUpstreamToolCaller_StoresUpstreamErrorInHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	serverCfg := &config.ServerConfig{
		Name: "time", URL: "http://localhost:1/mcp", Protocol: "streamable-http", Enabled: true,
	}
	require.NoError(t, store.SaveUpstreamServer(serverCfg))

	u := &upstreamToolCaller{
		logger:      zap.NewNop(),
		storage:     store,
		executionID: "exec-1",
	}

	start := time.Now()
	u.storeToolCallInHistory("time", "get_current_time",
		map[string]interface{}{"timezone": "Mars/Olympus"},
		isErrorResult("Invalid timezone: 'Mars/Olympus'"), nil, start, time.Millisecond)
	u.storeToolCallInHistory("time", "get_current_time",
		map[string]interface{}{"timezone": "UTC"},
		mcp.NewToolResultText("ok"), nil, start.Add(time.Second), time.Millisecond)

	records, err := store.GetServerToolCalls(storage.GenerateServerID(serverCfg), 10)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byErr := map[bool]*storage.ToolCallRecord{}
	for _, rec := range records {
		byErr[rec.Error != ""] = rec
	}
	require.Contains(t, byErr, true, "the isError call must persist an error message")
	assert.Contains(t, byErr[true].Error, "Mars/Olympus")
	require.Contains(t, byErr, false, "the successful call must stay clean")
}
