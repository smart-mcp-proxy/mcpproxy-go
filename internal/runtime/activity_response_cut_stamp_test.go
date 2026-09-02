package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// The write path for the direction stamp.
//
// Five review rounds tried to recover the response cut's direction at RENDER
// time and each was wrong about one emitter. It now travels ON the record, and
// these tests drive the real event handlers so a break shows up as a stored
// record rather than as a mock expectation.

// Both event handlers must persist the stamp the emitter published, unchanged.
func TestBothHandlersPersistTheEmittersStamp(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventType EventType
		payload   map[string]any
	}{
		{
			name:      "tool_call",
			eventType: EventTypeActivityToolCallCompleted,
			payload: map[string]any{
				"server_name": "upstream",
				"tool_name":   "big_tool",
				"status":      "success",
				"response":    "body",
			},
		},
		{
			name:      "internal_tool_call",
			eventType: EventTypeActivityInternalToolCall,
			payload: map[string]any{
				"internal_tool_name": "retrieve_tools",
				"status":             "success",
				"response":           "body",
			},
		},
	} {
		for _, cut := range []contracts.ResponseCut{
			contracts.CutShortenedAgentAndRecord,
			contracts.CutShortenedAgentOnly,
			contracts.CutShortenedRecordOnly,
		} {
			t.Run(tc.name+"/"+string(cut), func(t *testing.T) {
				store, cleanup := setupTestStorage(t)
				defer cleanup()

				svc := NewActivityService(store, zap.NewNop())

				payload := map[string]any{}
				for k, v := range tc.payload {
					payload[k] = v
				}
				payload["response_truncated"] = true
				payload["response_truncation_cut"] = string(cut)

				svc.handleEvent(Event{
					Type:      tc.eventType,
					Timestamp: time.Now().UTC(),
					Payload:   payload,
				})

				records := allActivities(t, store)
				require.Len(t, records, 1)
				assert.True(t, records[0].ResponseTruncated)
				assert.Equal(t, cut, records[0].ResponseTruncationCut,
					"the emitter's direction must reach BBolt, or every record reads as legacy")
			})
		}
	}
}

// The stamp is what DERIVES the boolean, so publishing a stamp alone is enough
// to flag the record. Anything else would let the two disagree.
func TestStampAloneSetsTheTruncatedFlag(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "t",
			"status":      "success",
			"response":    "body",
			// No "response_truncated" key at all.
			"response_truncation_cut": string(contracts.CutShortenedRecordOnly),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)
	assert.True(t, records[0].ResponseTruncated,
		"a stamp naming a cut IS a truncation claim; the boolean follows from it")
	assert.Equal(t, contracts.CutShortenedRecordOnly, records[0].ResponseTruncationCut)
}

// The unsafe direction of error, pinned: a payload that flags a cut with no
// stamp must keep the flag. Dropping it would make a consumer tokenize a
// partial body as if it were complete — an UNDERSTATEMENT of what was cut,
// silently. It is recorded unstamped instead, which the resolver answers
// without claiming a direction.
func TestUnstampedTruncationClaimIsKeptNotDropped(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":        "upstream",
			"tool_name":          "t",
			"status":             "success",
			"response":           "body",
			"response_truncated": true,
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)
	assert.True(t, records[0].ResponseTruncated,
		"never drop a truncation claim: the resulting record would read as complete")
	assert.Equal(t, contracts.CutNone, records[0].ResponseTruncationCut)

	resolved := contracts.ResolveResponseTruncation(
		records[0].ResponseTruncationCut, records[0].ResponseTruncated, records[0].ResponseStorageTruncated)
	assert.False(t, resolved.Stamped)
	assert.Equal(t, contracts.StoredVsDeliveredUnknown, resolved.Relation)
}

// A stamp from a NEWER core must not be stored as if this binary understood it.
// Storing it would let ResolveResponseTruncation's Valid() gate be the only
// thing between an unknown value and a rendered direction.
func TestUnrecognisedStampIsNotPersisted(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":             "upstream",
			"tool_name":               "t",
			"status":                  "success",
			"response":                "body",
			"response_truncated":      true,
			"response_truncation_cut": "some_future_cut",
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)
	assert.Equal(t, contracts.CutNone, records[0].ResponseTruncationCut)
	assert.True(t, records[0].ResponseTruncated, "the claim survives; only the unknown direction is dropped")
}

// The storage cut is independent of the stamp and must not overwrite it: a
// record can be cut twice, and the second cut says nothing about the first.
func TestStorageCutLeavesTheStampAlone(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":             "upstream",
			"tool_name":               "t",
			"status":                  "success",
			"response":                strings.Repeat("x", 50_000),
			"response_truncated":      true,
			"response_truncation_cut": string(contracts.CutShortenedAgentOnly),
		},
	})

	records := allActivities(t, store)
	require.Len(t, records, 1)
	assert.True(t, records[0].ResponseStorageTruncated)
	assert.True(t, records[0].ResponseTruncated)
	assert.Equal(t, contracts.CutShortenedAgentOnly, records[0].ResponseTruncationCut,
		"the storage cut must not restate, clear or overwrite the response cut's stamp")
}

// newEventCapturingRuntime returns a bare Runtime with a live subscription. The
// returned func waits for the first event of the given type and unsubscribes.
func newEventCapturingRuntime(t *testing.T) (*Runtime, func(EventType) *Event) {
	t.Helper()
	rt := &Runtime{logger: zap.NewNop(), eventSubs: make(map[chan Event]struct{})}
	ch := rt.SubscribeEvents()
	return rt, func(want EventType) *Event {
		t.Helper()
		defer rt.UnsubscribeEvents(ch)
		deadline := time.After(2 * time.Second)
		for {
			select {
			case evt := <-ch:
				if evt.Type == want {
					return &evt
				}
			case <-deadline:
				t.Fatalf("did not receive %s within timeout", want)
				return nil
			}
		}
	}
}

// Runtime.EmitActivityToolCallCompleted derives both wire keys from ONE typed
// value, which is what makes the compiler the primary guard: a caller cannot
// flag a cut without naming its direction. This pins the derivation rather than
// the type signature, so folding the two apart again is a test failure and not
// just a review comment.
func TestEmitterDerivesBothWireKeysFromOneValue(t *testing.T) {
	for _, cut := range []contracts.ResponseCut{
		contracts.CutNone,
		contracts.CutShortenedAgentAndRecord,
		contracts.CutShortenedAgentOnly,
		contracts.CutShortenedRecordOnly,
	} {
		rt, cleanup := newEventCapturingRuntime(t)

		rt.EmitActivityToolCallCompleted(
			"srv", "tool", "sess", "req", "mcp", "success", "", 1,
			nil, "body", cut, "", nil, "", "", 0, 0, "", nil, "")

		evt := cleanup(EventTypeActivityToolCallCompleted)
		require.NotNil(t, evt, string(cut))
		assert.Equal(t, cut.Cuts(), evt.Payload["response_truncated"], string(cut))
		assert.Equal(t, string(cut), evt.Payload["response_truncation_cut"], string(cut))
	}
}

// The internal path carries the same pair, from the same single value.
func TestInternalEmitterDerivesBothWireKeysFromOneValue(t *testing.T) {
	for _, cut := range []contracts.ResponseCut{
		contracts.CutNone,
		contracts.CutShortenedAgentOnly,
	} {
		rt, cleanup := newEventCapturingRuntime(t)

		rt.EmitActivityInternalToolCallTruncated(
			"retrieve_tools", "", "", "", "sess", "req", "success", "", 1,
			nil, "body", nil, "", cut)

		evt := cleanup(EventTypeActivityInternalToolCall)
		require.NotNil(t, evt, string(cut))
		assert.Equal(t, cut.Cuts(), evt.Payload["response_truncated"], string(cut))
		assert.Equal(t, string(cut), evt.Payload["response_truncation_cut"], string(cut))
	}
}
