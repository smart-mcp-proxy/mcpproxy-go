package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 124: an upstream_event row stores "error" in Status for a lost
// connection or a required login. It is not the outcome of a tool call, so it
// must not move the Errors tile — Usage "Errors" equals Activity errors +
// blocked (F1), and Usage never sees these rows. It belongs to the residual.
func TestActivitySummary_UpstreamEventIsNotACallError(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	base := getSummary(t, newCallParityServer(t, ts))

	records := append(parityRecords(ts),
		&storage.ActivityRecord{
			ID: "ue-1", Type: storage.ActivityTypeUpstreamEvent, ServerName: "github",
			Status: storage.ActivityStatusError, Timestamp: ts,
		},
		&storage.ActivityRecord{
			ID: "ue-2", Type: storage.ActivityTypeUpstreamEvent, ServerName: "github",
			Status: storage.ActivityStatusSuccess, Timestamp: ts,
		},
		// Spec 125: a failed skill load is not a tool call either; Usage never
		// counts it (review round 14, B1).
		&storage.ActivityRecord{
			ID: "sa-1", Type: storage.ActivityTypeSkillAccess, ServerName: "github",
			Status: storage.ActivityStatusError, Timestamp: ts,
		},
		// Spec 125: blocking a skill stores "blocked" as the row's status; it is
		// an administrator's decision, not a refused call (review round 15).
		&storage.ActivityRecord{
			ID: "sq-1", Type: storage.ActivityTypeSkillQuarantineChange, ServerName: "github",
			Status: storage.ActivityStatusBlocked, Timestamp: ts,
		},
	)
	srv := NewServer(&callParityController{
		activities: records,
		snap:       buildUsageSnapshot(records...),
	}, zap.NewNop().Sugar(), nil)
	got := getSummary(t, srv)

	assert.Equal(t, base.ErrorCount, got.ErrorCount, "a lost connection or a failed skill load is not a failed call")
	assert.Equal(t, base.SuccessCount, got.SuccessCount, "a recovery is not a successful call")
	assert.Equal(t, base.OtherCount+4, got.OtherCount)
	assert.Equal(t, base.TotalCount+4, got.TotalCount)
	assert.Equal(t, base.CallCount, got.CallCount)
	assert.Equal(t, base.BlockedCount, got.BlockedCount, "blocking a skill is not a refused call")

	_, usage := doUsageRequest(t, srv, "?window=24h")
	assert.EqualValues(t, got.ErrorCount+got.BlockedCount, usage.TotalErrors,
		"F1: Usage errors equal Activity errors + blocked")
}
