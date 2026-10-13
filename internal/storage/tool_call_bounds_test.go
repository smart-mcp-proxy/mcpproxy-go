package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The per-server "server_<id>_tool_calls" buckets were the other half of the
// 940MB config.db in #1173: RecordToolCall did CreateBucketIfNotExists + Put
// with no size cap, no count cap and no eviction, and ToolCallRecord.Response
// is a bare interface{} holding the whole upstream CallToolResult. One large
// response was persisted whole, per server, forever (#1176).
//
// These cases drive the real write path and assert against what actually
// landed in BBolt, so they fail on the pre-fix code rather than on a mock.

func newBoundsManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func bigToolCall(id, serverID string, responseSize int) *ToolCallRecord {
	return &ToolCallRecord{
		ID:         id,
		ServerID:   serverID,
		ServerName: serverID,
		ToolName:   "fetch",
		Arguments:  map[string]interface{}{"path": "/big"},
		Response:   map[string]interface{}{"text": strings.Repeat("x", responseSize)},
		Timestamp:  time.Now().UTC(),
	}
}

func TestRecordToolCallCapsOversizedResponse(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(4096, 0)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", "srv", 200_000)))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)

	encoded, err := json.Marshal(stored[0])
	require.NoError(t, err)
	assert.Less(t, len(encoded), 50_000,
		"a 200KB response must not be persisted whole under a 4KB cap")
	assert.True(t, stored[0].ResponseTruncated,
		"a record shortened on the write path must say so")
	assert.Greater(t, stored[0].ResponseBytes, int64(200_000),
		"ResponseBytes must record the size the upstream actually returned")

	// The field stays a JSON object. contracts.ToolCallRecord.Response is a
	// documented REST payload (GET /api/v1/tool-calls) and the masking pass
	// walks it as structured JSON, so retyping it to a bare string would be a
	// breaking API change.
	obj, ok := stored[0].Response.(map[string]interface{})
	require.True(t, ok, "Response must remain a JSON object, got %T", stored[0].Response)
	assert.Equal(t, true, obj["truncated"])
	assert.NotEmpty(t, obj["preview"], "a truncated record keeps a readable head of the payload")
}

func TestRecordToolCallKeepsSmallResponseVerbatim(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(64*1024, 0)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", "srv", 100)))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)

	assert.False(t, stored[0].ResponseTruncated)
	assert.Zero(t, stored[0].ResponseBytes, "an untruncated record carries no size annotation")
	obj, ok := stored[0].Response.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, strings.Repeat("x", 100), obj["text"], "a small response is stored verbatim")
}

// The caller keeps its own record after RecordToolCall returns — mcp.go reuses
// toolCallRecord for token accounting and code_execution keeps the parent
// record to link children. Capping must not reach back into it.
func TestRecordToolCallDoesNotMutateCallersRecord(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(1024, 0)

	record := bigToolCall("call-1", "srv", 100_000)
	require.NoError(t, mgr.RecordToolCall(record))

	assert.False(t, record.ResponseTruncated, "the caller's record must be untouched")
	obj, ok := record.Response.(map[string]interface{})
	require.True(t, ok)
	assert.Len(t, obj["text"], 100_000, "the caller still holds the whole response")
}

func TestRecordToolCallPrunesBucketToMaxRecords(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(64*1024, 5)

	base := time.Now().UTC()
	for i := 0; i < 12; i++ {
		rec := bigToolCall("call-"+string(rune('a'+i)), "srv", 10)
		rec.Timestamp = base.Add(time.Duration(i) * time.Millisecond)
		require.NoError(t, mgr.RecordToolCall(rec))
	}

	stored, err := mgr.GetServerToolCalls("srv", 100)
	require.NoError(t, err)
	assert.Len(t, stored, 5, "the bucket must be bounded to the configured record count")

	// GetServerToolCalls returns newest first, so the survivors are the tail.
	assert.Equal(t, "call-"+string(rune('a'+11)), stored[0].ID, "the newest call survives")
	for _, rec := range stored {
		assert.NotEqual(t, "call-a", rec.ID, "the oldest calls are the ones evicted")
	}
}

// Every writer must be bounded, not just the ones a caller remembered to cap.
// code_execution writes its parent record through the same seam under a
// synthetic server id.
func TestRecordToolCallBoundsCodeExecutionBucket(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(2048, 0)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("exec-1", "code_execution", 300_000)))

	stored, err := mgr.GetServerToolCalls("code_execution", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.True(t, stored[0].ResponseTruncated)
}

// registerServer stores a server and its identity, and returns the identity ID
// the tool-call bucket is actually keyed by.
//
// Using the ID rather than the name is load-bearing in these tests. mcp.go
// records with `ServerID: serverID` — the SHA-256 GenerateServerID computes
// from the server's attributes — never with the name. A test that passes the
// NAME as the ServerID makes the bucket key and the configured name identical,
// so a sweep comparing the two looks correct while deleting every live history
// in production. That is exactly what a live run caught.
func registerServer(t *testing.T, mgr *Manager, name string) string {
	t.Helper()
	cfg := &config.ServerConfig{Name: name, URL: "http://" + name, Protocol: "http", Enabled: true}
	require.NoError(t, mgr.SaveUpstreamServer(cfg))
	identity, err := mgr.RegisterServerIdentity(cfg, "/tmp/mcp_config.json")
	require.NoError(t, err)
	require.NotEqual(t, name, identity.ID, "the identity ID must not be the server name, or this test proves nothing")
	return identity.ID
}

// Deleting a server used to leave its whole call history behind: the store had
// twelve Delete* helpers and the deletion path invoked none of them.
func TestDeleteUpstreamServerDropsToolCallHistory(t *testing.T) {
	mgr := newBoundsManager(t)

	id := registerServer(t, mgr, "srv")
	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", id, 10)))

	before, err := mgr.GetServerToolCalls(id, 10)
	require.NoError(t, err)
	require.Len(t, before, 1)

	require.NoError(t, mgr.DeleteUpstreamServer("srv"))

	after, err := mgr.GetServerToolCalls(id, 10)
	require.NoError(t, err)
	assert.Empty(t, after, "removing a server must not leave its call history behind")
}

// The sweep takes NAMES but the buckets are keyed by identity, so it has to
// resolve one to the other. Comparing bucket keys against names directly marks
// every live history an orphan — a live run lost a real bucket that way.
func TestPruneOrphanToolCallsResolvesIdentitiesAndKeepsCodeExecution(t *testing.T) {
	mgr := newBoundsManager(t)

	keptID := registerServer(t, mgr, "kept")
	goneID := registerServer(t, mgr, "gone")

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", keptID, 10)))
	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-2", goneID, 10)))
	require.NoError(t, mgr.RecordToolCall(bigToolCall("exec-1", "code_execution", 10)))

	removed, err := mgr.PruneOrphanToolCalls([]string{"kept"})
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "only the unconfigured server's history is dropped")

	kept, err := mgr.GetServerToolCalls(keptID, 10)
	require.NoError(t, err)
	assert.Len(t, kept, 1, "a configured server keeps its history — its bucket is keyed by identity, not by name")

	gone, err := mgr.GetServerToolCalls(goneID, 10)
	require.NoError(t, err)
	assert.Empty(t, gone)

	exec, err := mgr.GetServerToolCalls(codeExecutionServerID, 10)
	require.NoError(t, err)
	assert.Len(t, exec, 1, "the synthetic code_execution bucket is never an orphan")
}

// An empty identity store means "not populated yet", not "every server is
// gone". Sweeping on that reading deletes live data on the next start.
func TestPruneOrphanToolCallsSkipsSweepWithNoIdentities(t *testing.T) {
	mgr := newBoundsManager(t)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", "0bf5be9861b76413", 10)))

	removed, err := mgr.PruneOrphanToolCalls([]string{"whatever"})
	require.NoError(t, err)
	assert.Zero(t, removed, "with no identities recorded the sweep must do nothing")

	stored, err := mgr.GetServerToolCalls("0bf5be9861b76413", 10)
	require.NoError(t, err)
	assert.Len(t, stored, 1)
}

// An operator who never configures the limits must still be bounded: the
// setter ignores non-positive values so an unset config cannot silently
// restore unbounded growth (the shape SetMaxResponseSize established in #1174).
func TestToolCallDefaultsApplyWithoutConfiguration(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(0, 0)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", "srv", 2*DefaultToolCallMaxResponseSize)))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.True(t, stored[0].ResponseTruncated,
		"the documented default cap applies when nothing configured one")
}

// A tool called with a megabyte of input persists that megabyte too: capping
// only the response leaves the record unbounded from the other side. The
// echo-shaped case (arguments ≈ response) makes it twice the intended size.
func TestRecordToolCallCapsOversizedArguments(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(4096, 0)

	rec := bigToolCall("call-1", "srv", 10)
	rec.Arguments = map[string]interface{}{"text": strings.Repeat("q", 200_000)}
	require.NoError(t, mgr.RecordToolCall(rec))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)

	encoded, err := json.Marshal(stored[0].Arguments)
	require.NoError(t, err)
	assert.Less(t, len(encoded), 50_000, "200KB of arguments must not be persisted whole under a 4KB cap")
	assert.Equal(t, true, stored[0].Arguments["truncated"])
	assert.NotEmpty(t, stored[0].Arguments["preview"])

	assert.Len(t, rec.Arguments["text"], 200_000, "the caller's arguments map must be untouched")
}

func TestRecordToolCallKeepsSmallArgumentsVerbatim(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(64*1024, 0)

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", "srv", 10)))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "/big", stored[0].Arguments["path"], "small arguments are stored verbatim")
}

// A cross-model review found this: the sweep skipped an identity whose JSON
// would not unmarshal but still counted the store as populated, so a live
// server's history — keyed by a hash the sweep could no longer resolve to a
// name — was treated as an orphan and deleted.
//
// The bucket KEY is the identity id (saveServerIdentity puts under
// identity.ID), so an unreadable record can still be spared.
func TestPruneOrphanToolCallsKeepsHistoryBehindAnUnreadableIdentity(t *testing.T) {
	mgr := newBoundsManager(t)

	keptID := registerServer(t, mgr, "kept")
	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-1", keptID, 10)))

	// A second, readable identity so the sweep does not abort for lack of any.
	registerServer(t, mgr, "other")

	// Corrupt the live server's identity record in place.
	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(serverIdentitiesBucket)).Put([]byte(keptID), []byte("{not json"))
	}))

	removed, err := mgr.PruneOrphanToolCalls([]string{"kept", "other"})
	require.NoError(t, err)
	assert.Zero(t, removed)

	stored, err := mgr.GetServerToolCalls(keptID, 10)
	require.NoError(t, err)
	assert.Len(t, stored, 1,
		"an identity that cannot be read is a reason to keep its history, not to delete it")
}

// The argument cap has a consequence beyond storage: POST
// /api/v1/tool-calls/{id}/replay re-dispatches a stored call's arguments when
// the caller supplies none. Replaying the {truncated, original_bytes, preview}
// placeholder would call the upstream tool with nonsense — and a
// call_tool_write or destructive replay reaches a real system. The record has
// to say so, so replay can refuse.
func TestTruncatedArgumentsAreFlaggedForReplay(t *testing.T) {
	mgr := newBoundsManager(t)
	mgr.SetToolCallLimits(4096, 0)

	rec := bigToolCall("call-1", "srv", 10)
	rec.Arguments = map[string]interface{}{"text": strings.Repeat("q", 200_000)}
	require.NoError(t, mgr.RecordToolCall(rec))

	stored, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.True(t, stored[0].ArgumentsTruncated,
		"a record whose arguments were shortened must be marked unreplayable")
	assert.False(t, rec.ArgumentsTruncated, "the caller's record stays untouched")

	require.NoError(t, mgr.RecordToolCall(bigToolCall("call-2", "srv", 10)))
	all, err := mgr.GetServerToolCalls("srv", 10)
	require.NoError(t, err)
	for _, c := range all {
		if c.ID == "call-2" {
			assert.False(t, c.ArgumentsTruncated, "a record with small arguments stays replayable")
		}
	}
}

// A cross-model review caught this: the placeholder carried a fixed 2KB
// preview whatever the cap was, so configuring a small cap produced a
// "truncated" record LARGER than the limit it was meant to enforce.
func TestTruncationPlaceholderFitsInsideASmallCap(t *testing.T) {
	// The filler is deliberately escape-heavy. An earlier version of this test
	// used plain "q"s and passed while the invariant was broken: the preview is
	// embedded as JSON inside JSON, so every quote and backslash is escaped a
	// second time. A 512-byte preview budget produced a 1243-byte record under
	// a 1024-byte cap; plain letters hide that entirely.
	filler := strings.Repeat("\"\\", 250_000)

	for _, cap := range []int{64, 256, 1024, 4096, 65536} {
		mgr := newBoundsManager(t)
		mgr.SetToolCallLimits(cap, 0)

		rec := bigToolCall("call-1", "srv", 10)
		rec.Response = map[string]interface{}{"text": filler}
		rec.Arguments = map[string]interface{}{"text": filler}
		require.NoError(t, mgr.RecordToolCall(rec))

		stored, err := mgr.GetServerToolCalls("srv", 10)
		require.NoError(t, err)
		require.Len(t, stored, 1)

		resp, err := json.Marshal(stored[0].Response)
		require.NoError(t, err)
		args, err := json.Marshal(stored[0].Arguments)
		require.NoError(t, err)

		assert.LessOrEqual(t, len(resp), cap,
			"cap=%d: the stored response placeholder must fit inside the configured cap, got %d bytes", cap, len(resp))
		assert.LessOrEqual(t, len(args), cap,
			"cap=%d: the stored arguments placeholder must fit inside the configured cap, got %d bytes", cap, len(args))
	}
}
