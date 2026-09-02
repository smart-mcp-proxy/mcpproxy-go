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

// prompt_get records became storage-truncatable when handlePromptGet started
// calling truncateForStorage, but EmitActivityPromptGet emitted no
// request_bytes / response_bytes — unlike the tool-call and internal-tool-call
// emitters, which have carried them since Spec 069 A1.
//
// That combination is not merely an accounting gap: ResponseBytes is the only
// surviving record of a shortened body's real size, and every surface that
// explains the cut (the CLI's storage notice, the drawer badge, the export)
// reads it. With ResponseBytes at 0 the pre-truncation size is unrecoverable
// from every surface, and the documented claim that response_bytes says how
// much was cut is simply false for this type.

// TestPromptGetEmitterCarriesByteCounts pins the emitter half.
func TestPromptGetEmitterCarriesByteCounts(t *testing.T) {
	logger := zap.NewNop()
	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	args := map[string]interface{}{"topic": "release notes"}
	response := map[string]interface{}{"messages": strings.Repeat("prompt text ", 500)}

	go rt.EmitActivityPromptGet("upstream", "big_prompt", "sess-1", "req-1", "success", "", 12, args, response)

	select {
	case evt := <-eventChan:
		require.Equal(t, EventTypeActivityPromptGet, evt.Type)

		requestBytes, ok := evt.Payload["request_bytes"]
		require.True(t, ok, "the prompt emitter must carry request_bytes like its siblings")
		assert.Positive(t, requestBytes)

		responseBytes, ok := evt.Payload["response_bytes"]
		require.True(t, ok, "the prompt emitter must carry response_bytes like its siblings")
		assert.Positive(t, responseBytes)
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.prompt.get event")
	}
}

// An empty prompt call must NOT emit a present zero: 0 means UNKNOWN across the
// whole activity log, and a present zero reads as a costless call. omitempty
// plus the >0 guard is what keeps "absent" and "measured as empty" distinct —
// exactly the convention the two sibling emitters follow.
func TestPromptGetEmitterOmitsByteCountsItCannotMeasure(t *testing.T) {
	rt := &Runtime{
		logger:    zap.NewNop(),
		eventSubs: make(map[chan Event]struct{}),
	}

	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	go rt.EmitActivityPromptGet("upstream", "empty_prompt", "sess-1", "req-1", "error", "boom", 3, nil, nil)

	select {
	case evt := <-eventChan:
		_, hasRequest := evt.Payload["request_bytes"]
		_, hasResponse := evt.Payload["response_bytes"]
		assert.False(t, hasRequest, "nothing to measure must stay absent, not a present zero")
		assert.False(t, hasResponse, "nothing to measure must stay absent, not a present zero")
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.prompt.get event")
	}
}

// TestPromptGetStorageTruncationKeepsThePreTruncationSize is the reason the
// emitter half matters: once the cap shortens the stored body, ResponseBytes is
// the ONLY thing left that says how much text there was.
func TestPromptGetStorageTruncationKeepsThePreTruncationSize(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityPromptGet,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":    "upstream",
			"prompt_name":    "big_prompt",
			"status":         "success",
			"response":       strings.Repeat("p", 50_000),
			"request_bytes":  int64(42),
			"response_bytes": int64(50_000),
		},
	})

	rec := onlyRecord(t, store)
	require.True(t, rec.ResponseStorageTruncated, "premise: the cap really did cut it")
	assert.Less(t, len(rec.Response), 50_000)
	assert.EqualValues(t, 50_000, rec.ResponseBytes,
		"without this the shortened body's real size is unrecoverable from every surface")
	assert.EqualValues(t, 42, rec.RequestBytes)
}

// End to end through the emitter, so a byte count dropped by EITHER half fails.
func TestPromptGetByteCountsSurviveEmitterToRecord(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := truncatingService(t, store, 1024)

	args := map[string]interface{}{"topic": "release notes"}
	response := strings.Repeat("p", 50_000)

	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	go rt.EmitActivityPromptGet("upstream", "big_prompt", "sess-1", "req-1", "success", "", 12, args, response)

	select {
	case evt := <-eventChan:
		svc.handleEvent(evt)
	case <-time.After(2 * time.Second):
		t.Fatal("no activity.prompt.get event")
	}

	rec := onlyRecord(t, store)
	require.Equal(t, storage.ActivityTypePromptGet, rec.Type)
	require.True(t, rec.ResponseStorageTruncated, "premise: the cap really did cut it")
	assert.Positive(t, rec.ResponseBytes,
		"a shortened prompt record with ResponseBytes 0 cannot say how much was cut")
	assert.Greater(t, rec.ResponseBytes, len(rec.Response),
		"the count is measured pre-truncation, so it must exceed the stored body")
	assert.Positive(t, rec.RequestBytes)
}
