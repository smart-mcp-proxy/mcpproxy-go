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
	)
	srv := NewServer(&callParityController{
		activities: records,
		snap:       buildUsageSnapshot(records...),
	}, zap.NewNop().Sugar(), nil)
	got := getSummary(t, srv)

	assert.Equal(t, base.ErrorCount, got.ErrorCount, "a lost connection is not a failed call")
	assert.Equal(t, base.SuccessCount, got.SuccessCount, "a recovery is not a successful call")
	assert.Equal(t, base.OtherCount+2, got.OtherCount)
	assert.Equal(t, base.TotalCount+2, got.TotalCount)
	assert.Equal(t, base.CallCount, got.CallCount)

	_, usage := doUsageRequest(t, srv, "?window=24h")
	assert.EqualValues(t, got.ErrorCount+got.BlockedCount, usage.TotalErrors,
		"F1: Usage errors equal Activity errors + blocked")
}
