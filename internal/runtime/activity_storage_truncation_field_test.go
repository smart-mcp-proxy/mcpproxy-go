package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ResponseTruncated and ResponseStorageTruncated describe OPPOSITE directions:
//
//	ResponseTruncated        recorded > delivered  (Spec 103)
//	ResponseStorageTruncated recorded < delivered  (activity_max_response_size)
//
// Three consumers act on the Spec 103 direction by REFUSING to count a record:
// usage_aggregate.go's truncatedBuiltinOverstatesDelivery drops its
// ResponseBytes from delivered traffic, and bench/replaycorpus withholds its
// response cost and any code-execution saving it belongs to. Storage truncation
// gives those consumers no reason to refuse anything — response_bytes is
// measured pre-truncation and stays honest — so folding the two into one bit
// makes them go dark on exactly the oversized records the cap exists to bound.
//
// These tests drive the real handleEvent path and assert on what BBolt holds.

func truncatingService(t *testing.T, store *storage.Manager, cap int) *ActivityService {
	t.Helper()
	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(cap)
	return svc
}

func onlyRecord(t *testing.T, store *storage.Manager) *storage.ActivityRecord {
	t.Helper()
	records, _, err := store.ListActivities(storage.ActivityFilter{Limit: 100})
	require.NoError(t, err)
	require.Len(t, records, 1)
	return records[0]
}

// TestStorageTruncationDoesNotSetSpec103Flag is the assertion that pins the
// split. call_tool_read is the population from issue #1173: the reporter's
// largest record was 10.75MB of proxied upstream payload.
func TestStorageTruncationDoesNotSetSpec103Flag(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "call_tool_read",
			"status":             "success",
			"response":           strings.Repeat("y", 50_000),
			"response_bytes":     int64(50_000),
			// No "response_truncated" key: the emitter did not claim the agent
			// received less than the log holds.
		},
	})

	rec := onlyRecord(t, store)
	assert.True(t, rec.ResponseStorageTruncated,
		"a record shortened on the write path must say so")
	assert.False(t, rec.ResponseTruncated,
		"storage truncation must not masquerade as the Spec 103 flag; that flag makes "+
			"the usage aggregate and the token benchmark discard the record's byte cost")
	assert.EqualValues(t, 50_000, rec.ResponseBytes,
		"response_bytes is measured pre-truncation and must stay honest")
}

// The emitter's own flag still has to survive, and it has to stay independent:
// a call_tool_* dispatch can be BOTH cut on the way to the agent and cut again
// on the way into the database.
func TestSpec103FlagAndStorageFlagAreIndependent(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "retrieve_tools",
			"status":             "success",
			"response":           strings.Repeat("y", 50_000),
			"response_truncated": true,
		},
	})

	rec := onlyRecord(t, store)
	assert.True(t, rec.ResponseTruncated, "storage truncation must not clear an emitter flag")
	assert.True(t, rec.ResponseStorageTruncated, "both directions can hold at once")
}

// An upstream tool_call takes the same split.
func TestToolCallStorageTruncationUsesTheStorageFlag(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "big_tool",
			"status":      "success",
			"response":    strings.Repeat("x", 50_000),
		},
	})

	rec := onlyRecord(t, store)
	assert.True(t, strings.HasSuffix(rec.Response, "...[truncated]"))
	assert.True(t, rec.ResponseStorageTruncated)
	assert.False(t, rec.ResponseTruncated,
		"the agent received the whole response; only the stored copy was cut")
}

// handlePromptGet had no truncation and no flag at all before this work, and
// still has no test of its own. Prompt content is upstream-controlled and can
// be just as large as a tool response.
func TestPromptGetStorageTruncationUsesTheStorageFlag(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityPromptGet,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"prompt_name": "big_prompt",
			"status":      "success",
			"response":    strings.Repeat("p", 50_000),
		},
	})

	rec := onlyRecord(t, store)
	assert.Less(t, len(rec.Response), 50_000, "prompt bodies take the same cap")
	assert.True(t, rec.ResponseStorageTruncated)
	assert.False(t, rec.ResponseTruncated,
		"the agent received the whole prompt; only the stored copy was cut")
}
