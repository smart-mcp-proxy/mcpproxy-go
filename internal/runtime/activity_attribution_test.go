package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func testAttribution() ActivityAttribution {
	return ActivityAttribution{
		Profile:       "work-readonly",
		ProfileSource: "pin",
		ClientID:      "cursor",
		ClientName:    "Cursor",
		TokenName:     "client-cursor",
		TokenPrefix:   "mcp_cli_abcd",
	}
}

func onlyRecord(t *testing.T, store *storage.Manager) *storage.ActivityRecord {
	t.Helper()
	f := storage.DefaultActivityFilter()
	f.ExcludeCallToolSuccess = false
	records, _, err := store.ListActivities(f)
	require.NoError(t, err)
	require.Len(t, records, 1)
	return records[0]
}

func TestActivityAttribution_PayloadOmitsEmptyAndCarriesPrefix(t *testing.T) {
	assert.Nil(t, ActivityAttribution{}.payload(), "an empty attribution adds no payload key")
	p := ActivityAttribution{Profile: "p", TokenPrefix: "mcp_agt_1234"}.payload()
	assert.Equal(t, map[string]any{"profile": "p", "_token_prefix": "mcp_agt_1234"}, p)
}

func TestActivityService_ToolCallRecordCarriesAttribution(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "list_issues", "session_id": "s1", "request_id": "r1",
			"source": "mcp", "status": "success", "duration_ms": int64(5),
			"profile":     "work-readonly", // legacy flat slug key stays -> metadata.profile
			"attribution": testAttribution().payload(),
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, "pin", rec.ProfileSource)
	assert.Equal(t, "cursor", rec.ClientID)
	assert.Equal(t, "Cursor", rec.ClientName)
	assert.Equal(t, "client-cursor", rec.TokenName)
	assert.Equal(t, "work-readonly", rec.Metadata["profile"], "metadata.profile is still written (Spec 057 SC-003)")
	assert.NotContains(t, rec.Metadata, "_token_prefix", "the token prefix never lands on the record")
}

func TestActivityService_PolicyDecisionBlockReasonFieldAndMetadata(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityPolicyDecision,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "create_issue", "session_id": "s1", "request_id": "r1",
			"decision": "blocked", "reason": "above tier cap", "block_reason": "profile_tier",
			"attribution": testAttribution().payload(),
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, "profile_tier", rec.BlockReason, "first-class field")
	assert.Equal(t, "profile_tier", rec.Metadata[storage.MetadataKeyBlockReason], "and the metadata key, for one release")
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, "client-cursor", rec.TokenName)
}

func TestActivityService_InternalToolCallAndPromptGetAttribution(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityInternalToolCall,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"internal_tool_name": "retrieve_tools", "session_id": "s1", "request_id": "r1",
			"status": "success", "attribution": testAttribution().payload(),
		},
	})
	rec := onlyRecord(t, store)
	assert.Equal(t, storage.ActivityTypeInternalToolCall, rec.Type)
	assert.Equal(t, "cursor", rec.ClientID)
	assert.Equal(t, "pin", rec.ProfileSource)

	store2, cleanup2 := setupTestStorage(t)
	defer cleanup2()
	svc2 := NewActivityService(store2, zap.NewNop())
	svc2.handleEvent(Event{
		Type:      EventTypeActivityPromptGet,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "prompt_name": "review", "session_id": "s1", "request_id": "r2",
			"status": "success", "attribution": testAttribution().payload(),
		},
	})
	rec = onlyRecord(t, store2)
	assert.Equal(t, storage.ActivityTypePromptGet, rec.Type)
	assert.Equal(t, "client-cursor", rec.TokenName)
	assert.Equal(t, "work-readonly", rec.Profile)
}

func TestActivityService_PayloadWithoutAttributionLeavesFieldsEmpty(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "list_issues", "status": "success", "duration_ms": int64(5),
		},
	})
	rec := onlyRecord(t, store)
	assert.Empty(t, rec.Profile)
	assert.Empty(t, rec.ProfileSource)
	assert.Empty(t, rec.ClientID)
	assert.Empty(t, rec.ClientName)
	assert.Empty(t, rec.TokenName)
	assert.Empty(t, rec.BlockReason)
}

func TestActivityService_ClientNameFallsBackToSessionResolver(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())
	svc.SetSessionClientResolver(func(string) (string, string) { return "claude-code", "1.0" })

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "github", "tool_name": "list_issues", "session_id": "s1", "status": "success",
		},
	})
	rec := onlyRecord(t, store)
	assert.Equal(t, "claude-code", rec.ClientName)
	assert.Equal(t, "claude-code", rec.Metadata["client_name"], "metadata.client_name stays")
}

// Attribution is display/filter data. It must not move the usage rollup: the
// persisted aggregate shape, its admission version and its numbers are the
// same with and without it.
func TestActivityService_AttributionDoesNotChangeUsageAdmission(t *testing.T) {
	assert.Equal(t, 6, usageAdmissionVersion, "usageAdmissionVersion is not bumped by Spec 108-e")

	mk := func(withAttr bool) *UsageAggregate {
		store, cleanup := setupTestStorage(t)
		defer cleanup()
		svc := NewActivityService(store, zap.NewNop())
		payload := map[string]any{
			"server_name": "github", "tool_name": "list_issues", "session_id": "s1", "request_id": "r1",
			"source": "mcp", "status": "success", "duration_ms": int64(5),
		}
		if withAttr {
			payload["attribution"] = testAttribution().payload()
		}
		svc.handleEvent(Event{Type: EventTypeActivityToolCallCompleted, Timestamp: time.Now().UTC(), Payload: payload})
		agg := NewUsageAggregate()
		agg.Apply(onlyRecord(t, store))
		return agg
	}
	a, b := mk(false), mk(true)
	require.Len(t, a.Tools, 1)
	require.Len(t, b.Tools, 1)
	for k, ta := range a.Tools {
		tb := b.Tools[k]
		require.NotNil(t, tb, k)
		assert.Equal(t, ta.Calls, tb.Calls)
		assert.Equal(t, ta.Errors, tb.Errors)
		assert.Equal(t, ta.LatencyBuckets, tb.LatencyBuckets)
	}
}

func TestNewUsageAggregate_IsFreshAndCurrentVersion(t *testing.T) {
	agg := NewUsageAggregate()
	require.NotNil(t, agg)
	assert.Equal(t, usageAdmissionVersion, agg.AdmissionVersion)
	assert.Empty(t, agg.Tools)
}
