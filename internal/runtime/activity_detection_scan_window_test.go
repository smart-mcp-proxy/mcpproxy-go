package runtime

import (
	"encoding/json"
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

// The per-record storage cap (activity_max_response_size, 64KB) and the
// sensitive-data detector's own cap (max_payload_size_kb, 1MB by default) are
// two independent budgets answering two different questions: how much text is
// worth KEEPING, and how much text is worth READING. Applying the storage
// answer to the reading question drops response scanning 16x and silently
// overrides an explicit operator setting — a secret past byte 65536 was
// reported before the cap was wired up and would not be after it.
//
// These tests pin the two apart by planting a credential BEYOND the storage cap
// but well inside the detector's, then asserting on the detection metadata the
// record carries. They fail if a handler ever truncates the same variable it
// hands to the detector.

// fakeAWSKey is the shape internal/security's own tests use: a syntactically
// valid AKIA key carrying no "EXAMPLE" marker, so the example-value filter
// cannot quietly drop it.
const fakeAWSKey = "AKIA1234567890ABCDEF"

// lowNoiseFiller is repetitive English rather than random bytes on purpose: the
// high-entropy category is on by default, and random padding would raise its
// own detections and let these tests pass without the planted credential ever
// being reached.
func lowNoiseFiller(n int) string {
	const unit = "the quick brown fox jumps over the lazy dog "
	return strings.Repeat(unit, n/len(unit)+1)[:n]
}

func detectingService(t *testing.T, store *storage.Manager) *ActivityService {
	t.Helper()
	svc := NewActivityService(store, zap.NewNop())
	detCfg := config.DefaultSensitiveDataDetectionConfig()
	detCfg.Enabled = true
	detCfg.ScanResponses = true
	svc.SetDetector(security.NewDetector(detCfg))
	return svc
}

// awsDetectionRecorded reports whether the record's detection metadata names an
// AWS access key. Asserting on the specific pattern rather than on "some
// detection happened" keeps the oracle honest: the high-entropy scanner can
// fire on the truncated prefix alone.
func awsDetectionRecorded(t *testing.T, rec *storage.ActivityRecord) bool {
	t.Helper()
	if rec.Metadata == nil {
		return false
	}
	raw, ok := rec.Metadata["sensitive_data_detection"]
	if !ok {
		return false
	}
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var payload struct {
		Detected   bool `json:"detected"`
		Detections []struct {
			Type string `json:"type"`
		} `json:"detections"`
	}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	if !payload.Detected {
		return false
	}
	for _, d := range payload.Detections {
		if d.Type == "aws_access_key" {
			return true
		}
	}
	return false
}

// awaitDrainedRecord stops the service — Stop waits on workersWG, which the
// detection goroutine is registered in — so the metadata write has landed by
// the time the record is read back.
func awaitDrainedRecord(t *testing.T, svc *ActivityService, store *storage.Manager) *storage.ActivityRecord {
	t.Helper()
	svc.Stop()
	records, _, err := store.ListActivities(storage.ActivityFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 1)
	return records[0]
}

// TestFullResponseReachesSensitiveDataDetector is the tool-call half.
func TestFullResponseReachesSensitiveDataDetector(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := detectingService(t, store)
	// Default cap (64KB) on purpose: the regression this guards is the DEFAULT
	// posture, not a hand-tuned one.
	response := lowNoiseFiller(70_000) + " aws_access_key_id=" + fakeAWSKey

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "dump_env",
			"status":      "success",
			"response":    response,
		},
	})

	rec := awaitDrainedRecord(t, svc, store)

	// Premise: the record really was cut, so the assertion below is not made
	// against an untruncated fixture.
	require.Less(t, len(rec.Response), len(response),
		"fixture must exceed the storage cap, or the assertion below proves nothing")

	assert.True(t, awsDetectionRecorded(t, rec),
		"a credential at byte ~70000 is inside the detector's max_payload_size_kb (1MB) "+
			"and must still be reported; scanning the storage-truncated text instead "+
			"cuts response coverage to activity_max_response_size (64KB)")
}

// TestFullPromptContentReachesSensitiveDataDetector is the prompts/get half.
// handlePromptGet's own doc comment says prompt content is upstream-controlled
// and carries the same injection/secret risk a tool response does, so it must
// get the same scan window.
func TestFullPromptContentReachesSensitiveDataDetector(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := detectingService(t, store)
	content := lowNoiseFiller(70_000) + " aws_access_key_id=" + fakeAWSKey

	svc.handleEvent(Event{
		Type:      EventTypeActivityPromptGet,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"prompt_name": "leaky_prompt",
			"status":      "success",
			"response":    content,
		},
	})

	rec := awaitDrainedRecord(t, svc, store)
	require.Less(t, len(rec.Response), len(content),
		"fixture must exceed the storage cap, or the assertion below proves nothing")

	assert.True(t, awsDetectionRecorded(t, rec),
		"prompt content must be scanned at the detector's cap, not the storage cap")
}
