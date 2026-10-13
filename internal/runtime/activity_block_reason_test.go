package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestActivityService_ToolCallCompletedPersistsBlockReason proves the typed
// block reason of a profile-refused code_execution sub-call (Spec 108 FR-029,
// T166) travels emit -> event -> record: on record.BlockReason and, for one
// release, on Metadata["block_reason"], next to parent_id and attribution.
// An empty reason must leave the legacy record shape byte-identical.
func TestActivityService_ToolCallCompletedPersistsBlockReason(t *testing.T) {
	cases := []struct {
		name        string
		blockReason string
	}{
		{name: "profile rule", blockReason: "profile_rule"},
		{name: "no reason", blockReason: ""},
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
				"github", "create_issue", "", "req-1", "internal", "blocked", "blocked by profile", 0,
				nil, "", false, "", nil, "", "", 0, 0,
				"", nil, "p-1", tc.blockReason,
				ActivityAttribution{Profile: "x", ProfileSource: "pin"},
				ActivityCallOutcome{},
			)

			select {
			case evt := <-sub:
				svc.handleEvent(evt)
			case <-time.After(time.Second):
				t.Fatal("no completion event published")
			}

			records, _, err := store.ListActivities(storage.DefaultActivityFilter())
			require.NoError(t, err)
			require.Len(t, records, 1)
			rec := records[0]
			assert.Equal(t, "p-1", rec.ParentID)
			assert.Equal(t, "x", rec.Profile)
			assert.Equal(t, tc.blockReason, rec.BlockReason)
			if tc.blockReason == "" {
				assert.Nil(t, rec.Metadata, "no reason and nothing else to record: Metadata stays nil, no empty map")
				return
			}
			assert.Equal(t, tc.blockReason, rec.Metadata[storage.MetadataKeyBlockReason])
		})
	}
}
