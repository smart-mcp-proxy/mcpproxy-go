package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 113-c FR-044/FR-045: the call-error taxonomy travels emit -> event
// (SSE payload) -> persisted record, and a completion without one leaves both
// the payload and the record untouched.
func TestActivityService_ToolCallCompletedPersistsCallOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome ActivityCallOutcome
		status  string
	}{
		{"http 502", ActivityCallOutcome{ErrorClass: "http", FaultDomain: "upstream", UpstreamHTTPStatus: 502}, "error"},
		{"tool_error without status", ActivityCallOutcome{ErrorClass: "tool_error", FaultDomain: "upstream"}, "error"},
		{"success carries none", ActivityCallOutcome{}, "success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := setupTestStorage(t)
			defer cleanup()
			svc := NewActivityService(store, zap.NewNop())

			rt := &Runtime{
				eventSubs:         make(map[chan Event]struct{}),
				internalEventSubs: make(map[chan Event]struct{}),
			}
			sub := rt.subscribeInternalEvents()
			defer rt.UnsubscribeEvents(sub)

			rt.EmitActivityToolCallCompletedAttributed(
				"github", "create_issue", "", "req-1", "mcp", tc.status, "boom", 5,
				nil, "", false, "", nil, "", "", 0, 0, "", nil, "", "",
				ActivityAttribution{}, tc.outcome,
			)

			select {
			case evt := <-sub:
				if tc.outcome.ErrorClass == "" {
					assert.NotContains(t, evt.Payload, "error_class")
					assert.NotContains(t, evt.Payload, "fault_domain")
					assert.NotContains(t, evt.Payload, "upstream_http_status")
				} else {
					assert.Equal(t, tc.outcome.ErrorClass, evt.Payload["error_class"])
					assert.Equal(t, tc.outcome.FaultDomain, evt.Payload["fault_domain"])
				}
				svc.handleEvent(evt)
			case <-time.After(time.Second):
				t.Fatal("no completion event published")
			}

			records, _, err := store.ListActivities(storage.DefaultActivityFilter())
			require.NoError(t, err)
			require.Len(t, records, 1)
			assert.Equal(t, tc.outcome.ErrorClass, records[0].ErrorClass)
			assert.Equal(t, tc.outcome.FaultDomain, records[0].FaultDomain)
			assert.Equal(t, tc.outcome.UpstreamHTTPStatus, records[0].UpstreamHTTPStatus)
		})
	}
}

// The activity filter selects on the taxonomy; legacy records match neither.
func TestActivityFilterMatchesTaxonomy(t *testing.T) {
	rec := &storage.ActivityRecord{ErrorClass: "http", FaultDomain: "upstream"}
	assert.True(t, (&storage.ActivityFilter{ErrorClass: "http"}).Matches(rec))
	assert.True(t, (&storage.ActivityFilter{FaultDomain: "upstream"}).Matches(rec))
	assert.False(t, (&storage.ActivityFilter{ErrorClass: "network"}).Matches(rec))
	assert.False(t, (&storage.ActivityFilter{FaultDomain: "proxy"}).Matches(rec))
	legacy := &storage.ActivityRecord{}
	assert.False(t, (&storage.ActivityFilter{ErrorClass: "http"}).Matches(legacy))
	assert.True(t, (&storage.ActivityFilter{}).Matches(legacy))
}

func TestActivityService_RecordToolCallRejectedStampsProxyPolicy(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.RecordToolCallRejected(newEvent(EventTypeActivityToolCallRejected, map[string]any{
		"server_name": "s", "tool_name": "t", "source": "mcp", "request_id": "r",
		"reason": "queue_full", "scope": "server", "message": "busy",
	}))
	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, storage.ActivityStatusRejected, records[0].Status)
	assert.Equal(t, "proxy_policy", records[0].ErrorClass)
	assert.Equal(t, "proxy", records[0].FaultDomain)
}
