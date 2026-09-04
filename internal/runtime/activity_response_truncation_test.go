package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
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
	assert.True(t, records[0].ResponseTruncated,
		"a record shortened on the write path must say so")
}

// call_tool_read proxies whole upstream payloads, which is where the largest
// records in a real database came from.
//
// The flag assertion is the interesting half. On an internal record
// ResponseTruncated means "the agent received LESS than ResponseBytes", and two
// cost consumers drop such rows from delivered traffic
// (truncatedBuiltinOverstatesDelivery, bench/replaycorpus). A built-in cut only
// on the way into the log was still delivered whole, so the flag must stay off
// or the row vanishes from the usage timeline with an honest ResponseBytes.
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
			"response_bytes":     int64(50_000),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.Less(t, len(records[0].Response), 50_000,
		"internal tool calls take the same cap as upstream calls")
	assert.True(t, strings.HasSuffix(records[0].Response, "...[truncated]"),
		"the stored text still shows it was cut")
	assert.False(t, records[0].ResponseTruncated,
		"a storage-only cut must not set the flag that excludes the row from delivered bytes")
}

// The delivered-bytes consequence of the assertion above, driven end to end:
// a built-in cut only for storage must still be counted in the usage timeline.
// Mirrors TestUsageAggregate_TruncatedBuiltinDoesNotInflateDeliveredBytes from
// the other side — that one proves an emitter-flagged record is excluded, this
// one proves a storage-cut record is not.
func TestStorageTruncatedBuiltinStillCountsDeliveredBytes(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

	svc.handleEvent(Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "describe_tool",
			"status":             "success",
			"response":           strings.Repeat("z", 80_000),
			"response_bytes":     int64(80_000),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)
	require.EqualValues(t, 80_000, records[0].ResponseBytes,
		"ResponseBytes describes what the agent received, before any storage cut")

	agg := newUsageAggregate()
	agg.Apply(records[0])
	var delivered int64
	for _, b := range agg.Buckets {
		delivered += b.RespBytesSum
	}
	assert.EqualValues(t, 80_000, delivered,
		"describe_tool delivers its response whole; only the stored copy was cut")
}

// handlePromptGet builds its record separately from the tool paths, so it needs
// its own case — the rest of the package passes with only this path neutered.
func TestPromptGetResponseTruncatedForStorage(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

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

	records := allActivities(t, store)
	require.Len(t, records, 1)

	assert.Less(t, len(records[0].Response), 50_000,
		"prompts take the same per-record cap as tool responses")
	assert.True(t, strings.HasSuffix(records[0].Response, "...[truncated]"))
	assert.True(t, records[0].ResponseTruncated,
		"on a prompt record the flag means the stored copy was cut, as on tool_call")
}

// An emitter-set response_truncated flag (Spec 103: recorded response larger
// than the one the agent received) must survive a record that storage does NOT
// shorten. The two meanings are OR-ed, so neither may clear the other.
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
	assert.True(t, records[0].ResponseTruncated)
}

// SetMaxResponseSize takes the same "ignore non-positive" shape as the other
// retention setters, so a zero-valued config cannot silently disable the cap.
func TestSetMaxResponseSizeIgnoresNonPositive(t *testing.T) {
	svc := NewActivityService(nil, zap.NewNop())
	require.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize)

	svc.SetMaxResponseSize(0)
	assert.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize,
		"an unset config value must leave the default in place")

	svc.SetMaxResponseSize(-1)
	assert.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize)

	svc.SetMaxResponseSize(2048)
	assert.Equal(t, 2048, svc.maxResponseSize)
}

// Storage truncation must not narrow the Spec 026 scan window.
//
// The detector has its own, far larger cap (sensitive_data_detection.
// max_payload_size_kb, 1024KB by default). If the response were cut to
// activity_max_response_size (64KB) before the scan, every secret past that
// boundary would silently stop being detected — a 16x smaller window in the
// DEFAULT configuration, with nothing recording that it had moved. Both
// handlers therefore scan the text as received and store the cut copy.
//
// Each case puts the key AFTER the storage cut, so it is absent from the
// persisted response and can only be found via the pre-truncation string.
func TestDetectionScansUntruncatedResponse(t *testing.T) {
	const awsKey = "AKIA1234567890ABCDEF"
	// Comfortably past the 64KB default cap, well inside the detector's 1024KB.
	padding := strings.Repeat("x", storage.DefaultMaxResponseSize+10_000)

	t.Run("tool_call", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()

		svc := NewActivityService(store, zap.NewNop())
		svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))

		svc.handleEvent(Event{
			Type:      EventTypeActivityToolCallCompleted,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name": "upstream",
				"tool_name":   "dump_config",
				"status":      "success",
				"response":    padding + " trailing secret " + awsKey,
			},
		})

		det := waitForDetectionMetadata(t, store, true)
		assert.Equal(t, true, det["detected"],
			"a secret past the storage cut must still be detected")

		records := allActivities(t, store)
		require.Len(t, records, 1)
		assert.NotContains(t, records[0].Response, awsKey,
			"the stored copy is still truncated — the scan is what sees the whole text")
	})

	t.Run("prompt_get", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()

		svc := NewActivityService(store, zap.NewNop())
		svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))

		svc.handleEvent(Event{
			Type:      EventTypeActivityPromptGet,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name": "upstream",
				"prompt_name": "render_context",
				"status":      "success",
				"response":    padding + " trailing secret " + awsKey,
			},
		})

		det := waitForDetectionMetadata(t, store, true)
		assert.Equal(t, true, det["detected"])

		records := allActivities(t, store)
		require.Len(t, records, 1)
		assert.NotContains(t, records[0].Response, awsKey)
	})
}
