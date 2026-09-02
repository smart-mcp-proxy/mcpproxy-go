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

// allActivities lists without DefaultActivityFilter's ExcludeCallToolSuccess,
// which would drop the successful call_tool_* records this file is about.
func allActivities(t *testing.T, store *storage.Manager) []*storage.ActivityRecord {
	t.Helper()
	records, _, err := store.ListActivities(storage.ActivityFilter{Limit: 100})
	require.NoError(t, err)
	return records
}

// Retention bounds the activity log by age, count and TOTAL size, but nothing
// bounded a SINGLE record: activity_max_response_size was declared in config and
// never read, and TruncateActivityResponse had no production caller. A proxied
// upstream response was persisted whole, so a handful of large calls could add
// hundreds of MB to config.db with nothing logged.
//
// Each case drives the real event path and asserts against what actually landed
// in storage, so it fails on the pre-fix code rather than on a mock.

func TestToolCallResponseTruncatedForStorage(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

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

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.Less(t, len(records[0].Response), 50_000,
		"a 50KB response must not be persisted whole under a 1KB cap")
	assert.True(t, strings.HasSuffix(records[0].Response, "...[truncated]"),
		"truncation must be visible in the stored text")
	assert.True(t, records[0].ResponseStorageTruncated,
		"a record shortened on the write path must say so")
}

// call_tool_read proxies whole upstream payloads, which is where the largest
// records in a real database came from.
func TestInternalToolCallResponseTruncatedForStorage(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

	svc.handleEvent(Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "call_tool_read",
			"status":             "success",
			"response":           strings.Repeat("y", 50_000),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.Less(t, len(records[0].Response), 50_000,
		"internal tool calls take the same cap as upstream calls")
	assert.True(t, records[0].ResponseStorageTruncated)
}

// An emitter-set response_truncated flag (Spec 103: recorded response larger
// than the one the agent received) must survive a record that storage does NOT
// shorten. The two meanings live on SEPARATE fields, so neither may clear the
// other — see activity_storage_truncation_field_test.go for the other half.
func TestEmitterTruncationFlagSurvivesUntruncatedRecord(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":        "upstream",
			"tool_name":          "small_tool",
			"status":             "success",
			"response":           "short",
			"response_truncated": true,
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.Equal(t, "short", records[0].Response, "a short response is stored verbatim")
	assert.True(t, records[0].ResponseTruncated,
		"storage truncation must not clear a flag the emitter set")
}

// Guards the default: an operator who never sets activity_max_response_size
// still gets the documented 64KB bound rather than unbounded growth.
func TestDefaultResponseCapAppliesWithoutConfig(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "huge_tool",
			"status":      "success",
			"response":    strings.Repeat("z", DefaultActivityMaxResponseSize*3),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.LessOrEqual(t, len(records[0].Response),
		DefaultActivityMaxResponseSize+len("...[truncated]"),
		"the 64KB default must bound a record with no explicit config")
	assert.True(t, records[0].ResponseStorageTruncated)
}

// SetMaxResponseSize takes SetRetentionConfig's maxSizeBytes convention rather
// than the "ignore non-positive" shape of the setters above it: 0 DISABLES the
// cap and -1 means "leave unchanged". See TestZeroCapStoresTheResponseWhole for
// the end-to-end half — the convention is only worth anything if the truncation
// path honours it too.
func TestSetMaxResponseSizeConvention(t *testing.T) {
	svc := NewActivityService(nil, zap.NewNop())
	require.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize)

	svc.SetMaxResponseSize(-1)
	assert.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize,
		"a negative sentinel must leave the current cap alone")

	svc.SetMaxResponseSize(2048)
	assert.Equal(t, 2048, svc.maxResponseSize)

	svc.SetMaxResponseSize(0)
	assert.Equal(t, 0, svc.maxResponseSize,
		"an explicit 0 must reach the service; it is how an operator disables the cap")
}
