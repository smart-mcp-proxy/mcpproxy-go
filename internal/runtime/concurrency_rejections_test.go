package runtime

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestHandleToolCallRejected_PersistsRejectedRecord is the FR-012 storage
// contract: a shed lands as a tool_call record with the dedicated "rejected"
// status and the metadata an operator needs to right-size limits.
func TestHandleToolCallRejected_PersistsRejectedRecord(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())

	svc.RecordToolCallRejected(Event{
		Type:      EventTypeActivityToolCallRejected,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":    "analytics-db",
			"tool_name":      "query",
			"source":         "internal",
			"request_id":     "req-42",
			"reason":         "queue_timeout",
			"scope":          "server",
			"message":        "Server \"analytics-db\" is busy",
			"limit":          2,
			"retry_after_ms": int64(30000),
			"duration_ms":    int64(30001),
		},
	})

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)

	rec := records[0]
	assert.Equal(t, storage.ActivityTypeToolCall, rec.Type)
	assert.Equal(t, storage.ActivityStatusRejected, rec.Status)
	assert.Equal(t, "analytics-db", rec.ServerName)
	assert.Equal(t, "query", rec.ToolName)
	assert.Equal(t, storage.ActivitySource("internal"), rec.Source)
	assert.Equal(t, "req-42", rec.RequestID)
	assert.Equal(t, int64(30001), rec.DurationMs)

	require.NotNil(t, rec.Metadata)
	assert.Equal(t, "queue_timeout", rec.Metadata[storage.MetadataKeyRejectionReason])
	assert.Equal(t, "server", rec.Metadata[storage.MetadataKeyRejectionScope])
	assert.EqualValues(t, 2, toInt64(rec.Metadata[storage.MetadataKeyRejectionLimit]))
	assert.EqualValues(t, 30000, toInt64(rec.Metadata[storage.MetadataKeyRejectionRetryAfterMs]))
}

// TestRejectedRecordIsWrittenOnceOffTheBus guards the non-lossy path: the row is
// written at the rejection site, and the event-bus copy — which exists only so
// live subscribers see the shed — must not write a second one.
func TestRejectedRecordIsWrittenOnceOffTheBus(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	evt := Event{
		Type:      EventTypeActivityToolCallRejected,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "db",
			"tool_name":   "query",
			"reason":      "queue_full",
			"scope":       "server",
		},
	}

	svc.RecordToolCallRejected(evt)
	svc.handleEvent(evt)

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	assert.Len(t, records, 1, "the bus copy must not persist a duplicate rejected row")
}

// toInt64 normalises a JSON-round-tripped number (float64) or a native int64.
func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return -1
	}
}

// TestUsageAggregate_RejectedDoesNotInflateCalls covers the usage-aggregation
// half of FR-012: a shed never executed, so it must not pollute call counts,
// latency percentiles or the executed-call timeline.
func TestUsageAggregate_RejectedDoesNotInflateCalls(t *testing.T) {
	agg := newUsageAggregate()

	agg.Apply(&storage.ActivityRecord{
		Type:       storage.ActivityTypeToolCall,
		ServerName: "db",
		ToolName:   "query",
		Status:     storage.ActivityStatusSuccess,
		DurationMs: 12,
		Timestamp:  time.Now().UTC(),
	})
	agg.Apply(&storage.ActivityRecord{
		Type:       storage.ActivityTypeToolCall,
		ServerName: "db",
		ToolName:   "query",
		Status:     storage.ActivityStatusRejected,
		DurationMs: 30000, // the queue wait, not an execution time
		Timestamp:  time.Now().UTC(),
	})

	tu := agg.tool("db", "query")
	assert.EqualValues(t, 1, tu.Calls, "a shed never executed and must not count as a call")
	assert.EqualValues(t, 0, tu.Errors, "a shed is not an upstream error")
	assert.EqualValues(t, 1, tu.Rejected)

	var timelineCalls int64
	for _, b := range agg.Timeline() {
		timelineCalls += b.Calls
	}
	assert.EqualValues(t, 1, timelineCalls, "the executed-call timeline must exclude sheds")
}

// TestActivitySourceFromContext maps request sources onto the activity-log
// vocabulary. An unset source means the call never crossed an external surface
// — code execution or activity replay — which is "internal", not "mcp".
func TestActivitySourceFromContext(t *testing.T) {
	assert.Equal(t, "cli", activitySourceFromContext(reqcontext.WithRequestSource(context.Background(), reqcontext.SourceCLI)))
	assert.Equal(t, "api", activitySourceFromContext(reqcontext.WithRequestSource(context.Background(), reqcontext.SourceRESTAPI)))
	assert.Equal(t, "mcp", activitySourceFromContext(reqcontext.WithRequestSource(context.Background(), reqcontext.SourceMCP)))
	assert.Equal(t, "internal", activitySourceFromContext(context.Background()))
}

// TestReplayToolCall_CreatesNoExecutionTimeoutBeforeDispatch is the FR-005
// structural guard for activity replay.
//
// Replay used to wrap client.CallTool in a CallToolTimeout context created
// BEFORE dispatch, so any time the call spent waiting in a concurrency-limiter
// queue was subtracted from its execution budget. The execution timeout now
// starts after admission (inside core.Client.CallTool), and the only way to
// keep it that way is to assert replay does not re-introduce a pre-dispatch
// deadline — a behavioural test cannot see the difference without a real slow
// upstream.
func TestReplayToolCall_CreatesNoExecutionTimeoutBeforeDispatch(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runtime.go", nil, parser.ParseComments)
	require.NoError(t, err)

	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ReplayToolCall" || fn.Body == nil {
			return true
		}
		body = fn.Body
		return false
	})
	require.NotNil(t, body, "ReplayToolCall not found in runtime.go")

	var foundTimeout, foundCall bool
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "context" && (sel.Sel.Name == "WithTimeout" || sel.Sel.Name == "WithDeadline") {
			foundTimeout = true
		}
		if pkg.Name == "client" && sel.Sel.Name == "CallTool" {
			foundCall = true
		}
		return true
	})

	assert.True(t, foundCall, "replay must still dispatch through the managed client (that is where admission lives)")
	assert.False(t, foundTimeout,
		"ReplayToolCall must not create an execution deadline before dispatch: queue wait would eat the execution budget (FR-005)")
}

// TestReplayToolCall_ReleasesRuntimeLockBeforeDispatch is the FR-008 structural
// guard for the same function. Replay held r.mu.RLock for its whole duration,
// so once replay started queueing behind a concurrency limit it also blocked
// ApplyConfig and every other writer for the queue-plus-execution duration.
// The lock must be released before the dispatch, not deferred past it.
func TestReplayToolCall_ReleasesRuntimeLockBeforeDispatch(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runtime.go", nil, parser.ParseComments)
	require.NoError(t, err)

	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ReplayToolCall" || fn.Body == nil {
			return true
		}
		body = fn.Body
		return false
	})
	require.NotNil(t, body, "ReplayToolCall not found in runtime.go")

	isMuCall := func(call *ast.CallExpr, method string) bool {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return false
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		return ok && inner.Sel.Name == "mu"
	}

	var unlockPos, callPos token.Pos
	var deferredUnlock bool
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.DeferStmt:
			if isMuCall(node.Call, "RUnlock") || isMuCall(node.Call, "Unlock") {
				deferredUnlock = true
			}
		case *ast.CallExpr:
			if isMuCall(node, "RUnlock") && !unlockPos.IsValid() {
				unlockPos = node.Pos()
			}
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CallTool" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "client" {
					callPos = node.Pos()
				}
			}
		}
		return true
	})

	assert.False(t, deferredUnlock,
		"ReplayToolCall must not defer the runtime lock past the upstream call (FR-008)")
	require.True(t, unlockPos.IsValid(), "ReplayToolCall must release the runtime read lock explicitly")
	require.True(t, callPos.IsValid(), "ReplayToolCall must dispatch through the managed client")
	assert.Less(t, int(unlockPos), int(callPos),
		"the runtime lock must be released BEFORE dispatch, so a queued replay cannot stall config reload (FR-008)")
}
