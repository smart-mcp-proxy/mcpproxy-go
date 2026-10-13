package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestHandleToolCallCompleted_ToonOutputMetadata (spec 084 T017, FR-010): the
// toon_output payload lands verbatim in the tool_call record metadata, keyed
// per text-block index with byte sizes.
func TestHandleToolCallCompleted_ToonOutputMetadata(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())

	toon := map[string]interface{}{
		"mode": "adaptive",
		"blocks": []interface{}{
			map[string]interface{}{
				"index": 0, "outcome": "encoded", "classification": "tabular",
				"bytes_before": 8123, "bytes_after": 5140, "threshold_pct": 15,
			},
		},
	}

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "list_repos",
			"status":      "success",
			"duration_ms": int64(10),
			"response":    "encoded body",
			"toon_output": toon,
		},
	})

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)

	md := records[0].Metadata
	require.NotNil(t, md, "toon_output alone must be enough to create metadata")
	got, ok := md["toon_output"].(map[string]interface{})
	require.True(t, ok, "metadata.toon_output must be a map, got %T", md["toon_output"])
	assert.Equal(t, "adaptive", got["mode"])
	blocks, ok := got["blocks"].([]interface{})
	require.True(t, ok)
	require.Len(t, blocks, 1)
	b0 := blocks[0].(map[string]interface{})
	assert.Equal(t, "encoded", b0["outcome"])
}

// TestHandleToolCallCompleted_NoToonMetadataWhenOff (SC-002): without a
// toon_output payload key the record metadata carries no toon_output entry —
// the disabled path is record-identical to pre-feature behavior.
func TestHandleToolCallCompleted_NoToonMetadataWhenOff(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github",
			"tool_name":   "list_repos",
			"status":      "success",
			"duration_ms": int64(10),
			"response":    `{"repos": []}`,
		},
	})

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)
	if records[0].Metadata != nil {
		_, has := records[0].Metadata["toon_output"]
		assert.False(t, has, "off-mode records must carry no toon_output metadata key")
	}
}

// waitForDetectionMetadata polls until the async detection worker has (or has
// not) attached sensitive_data_detection metadata to the single stored record.
func waitForDetectionMetadata(t *testing.T, store *storage.Manager, want bool) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		records, _, err := store.ListActivities(storage.DefaultActivityFilter())
		require.NoError(t, err)
		require.Len(t, records, 1)
		if records[0].Metadata != nil {
			if det, ok := records[0].Metadata["sensitive_data_detection"].(map[string]interface{}); ok {
				if !want {
					t.Fatalf("expected NO detection metadata, got %v", det)
				}
				return det
			}
		}
		if time.Now().After(deadline) {
			if want {
				t.Fatal("timed out waiting for detection metadata")
			}
			return nil // grace period elapsed without a detection — as expected
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunAsyncDetection_ScansDetectionText (spec 084 T033, FR-007b): when the
// event carries a non-empty detection_text, the detector scans THAT — not the
// agent-facing response. Proven in both directions: a secret only in
// detection_text is found; a secret only in the (TOON-encoded) response is
// not scanned when detection_text is present.
func TestRunAsyncDetection_ScansDetectionText(t *testing.T) {
	const awsKey = "AKIA1234567890ABCDEF"

	t.Run("secret in detection_text is found", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))

		svc.handleEvent(Event{
			Type:      EventTypeActivityToolCallCompleted,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name":    "github",
				"tool_name":      "list_secrets",
				"status":         "success",
				"duration_ms":    int64(5),
				"response":       "marker\ncompact toon body without the key",
				"detection_text": "pre-encoding text with " + awsKey,
			},
		})

		det := waitForDetectionMetadata(t, store, true)
		assert.Equal(t, true, det["detected"])
	})

	t.Run("detection_text replaces response as scan input", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))

		svc.handleEvent(Event{
			Type:      EventTypeActivityToolCallCompleted,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name":    "github",
				"tool_name":      "list_secrets",
				"status":         "success",
				"duration_ms":    int64(5),
				"response":       "response containing " + awsKey,
				"detection_text": "clean pre-encoding rendering",
			},
		})

		// Allow the async worker to run; no detection may appear because the
		// scan input was detection_text, which is clean.
		time.Sleep(300 * time.Millisecond)
		records, _, err := store.ListActivities(storage.DefaultActivityFilter())
		require.NoError(t, err)
		require.Len(t, records, 1)
		if records[0].Metadata != nil {
			_, has := records[0].Metadata["sensitive_data_detection"]
			assert.False(t, has, "response must NOT be scanned when detection_text is present")
		}
	})

	t.Run("empty detection_text falls back to response (off path unchanged)", func(t *testing.T) {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		svc.SetDetector(security.NewDetector(config.DefaultSensitiveDataDetectionConfig()))

		svc.handleEvent(Event{
			Type:      EventTypeActivityToolCallCompleted,
			Timestamp: time.Now().UTC(),
			Payload: map[string]any{
				"server_name": "github",
				"tool_name":   "list_secrets",
				"status":      "success",
				"duration_ms": int64(5),
				"response":    "response containing " + awsKey,
			},
		})

		det := waitForDetectionMetadata(t, store, true)
		assert.Equal(t, true, det["detected"])
	})
}
