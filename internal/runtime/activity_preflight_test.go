package runtime

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func newPreflightActivityService(t *testing.T) (*ActivityService, *storage.Manager) {
	t.Helper()
	mgr, cleanup := setupTestStorage(t)
	t.Cleanup(cleanup)
	return NewActivityService(mgr, zap.NewNop()), mgr
}

// RecordPreflight must be DURABLE by the time it returns — the served surface
// answers 200 on the strength of that (FR-014). The service's async event loop
// is never started here on purpose: if the write went through the bounded event
// channel, this test would find no record at all.
func TestRecordPreflightWritesSynchronously(t *testing.T) {
	svc, mgr := newPreflightActivityService(t)

	err := svc.RecordPreflight(PreflightActivity{
		RequestID: "req-sync",
		Source:    storage.ActivitySourceCLI,
		Verdict:   preflight.VerdictBlocked,
		Timestamp: time.Now(),
		Tools: []PreflightToolOutcome{
			{ID: "gh:create_issue", Status: preflight.StatusReady},
			{ID: "slack:post", Status: preflight.StatusUnavailable, Reason: preflight.ReasonServerQuarantined},
			{ID: "slack:read", Status: preflight.StatusUnavailable, Reason: preflight.ReasonServerQuarantined},
		},
	})
	require.NoError(t, err)

	filter := storage.DefaultActivityFilter()
	filter.Types = []string{string(storage.ActivityTypePreflight)}
	records, total, err := mgr.ListActivities(filter)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, storage.ActivityTypePreflight, record.Type)
	assert.Equal(t, "req-sync", record.RequestID)
	assert.Equal(t, storage.ActivitySourceCLI, record.Source)
	// Non-ready verdict maps onto the existing closed status vocabulary.
	assert.Equal(t, storage.ActivityStatusBlocked, record.Status)
	assert.Contains(t, storage.ValidActivityStatuses, record.Status)

	assert.Equal(t, preflight.VerdictBlocked, record.Metadata[storage.MetadataKeyPreflightVerdict])
	assert.InDelta(t, 3, record.Metadata[storage.MetadataKeyPreflightIDsCount], 0.0001)

	reasons, ok := record.Metadata[storage.MetadataKeyPreflightReasons].(map[string]interface{})
	require.True(t, ok, "reasons must survive the BBolt JSON round trip")
	assert.InDelta(t, 2, reasons[preflight.ReasonServerQuarantined], 0.0001)

	perTool, ok := record.Metadata[storage.MetadataKeyPreflightPerTool].([]interface{})
	require.True(t, ok)
	require.Len(t, perTool, 3)
	first, ok := perTool[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "gh:create_issue", first[storage.PreflightPerToolKeyID])
	assert.Equal(t, preflight.StatusReady, first[storage.PreflightPerToolKeyStatus])
	_, hasReason := first[storage.PreflightPerToolKeyReason]
	assert.False(t, hasReason, "a ready tool carries no reason")
}

func TestRecordPreflightReadyVerdictIsSuccess(t *testing.T) {
	svc, mgr := newPreflightActivityService(t)

	require.NoError(t, svc.RecordPreflight(PreflightActivity{
		RequestID: "req-ready",
		Verdict:   preflight.VerdictReady,
		Tools:     []PreflightToolOutcome{{ID: "gh:create_issue", Status: preflight.StatusReady}},
	}))

	filter := storage.DefaultActivityFilter()
	filter.RequestID = "req-ready"
	records, _, err := mgr.ListActivities(filter)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, storage.ActivityStatusSuccess, records[0].Status)
	// Source defaults to the REST surface when the caller does not set one.
	assert.Equal(t, storage.ActivitySourceAPI, records[0].Source)
}

// A failed write must PROPAGATE: the handler turns it into a 503 rather than
// answering 200 with no record (FR-008/FR-014).
func TestRecordPreflightPropagatesWriteFailure(t *testing.T) {
	svc, mgr := newPreflightActivityService(t)
	require.NoError(t, mgr.Close())

	err := svc.RecordPreflight(PreflightActivity{
		RequestID: "req-fail",
		Verdict:   preflight.VerdictReady,
		Tools:     []PreflightToolOutcome{{ID: "gh:create_issue", Status: preflight.StatusReady}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save preflight activity record")
}

func TestRecordPreflightWithoutStorageIsUnavailable(t *testing.T) {
	var nilService *ActivityService
	assert.ErrorIs(t, nilService.RecordPreflight(PreflightActivity{}), ErrActivityUnavailable)

	svc := NewActivityService(nil, zap.NewNop())
	assert.ErrorIs(t, svc.RecordPreflight(PreflightActivity{}), ErrActivityUnavailable)
}

// Once the service has closed its write barrier the DB may be closing, so a late
// preflight must be refused rather than risk a write racing the close.
func TestRecordPreflightAfterStopIsRefused(t *testing.T) {
	svc, _ := newPreflightActivityService(t)
	svc.Stop()

	err := svc.RecordPreflight(PreflightActivity{RequestID: "req-late", Verdict: preflight.VerdictReady})
	assert.ErrorIs(t, err, ErrActivityShuttingDown)
}

// Spec 099 FR-013: the in-band record renders the raw request under its own
// metadata key, so the raw requested count is recoverable from the record even
// though ids_count reports the unique one.
func TestRecordPreflightRawArgumentsSurviveTheRoundTrip(t *testing.T) {
	svc, mgr := newPreflightActivityService(t)

	require.NoError(t, svc.RecordPreflight(PreflightActivity{
		RequestID: "req-args",
		Source:    storage.ActivitySourceMCP,
		Surface:   storage.PreflightSurfaceMCPCheck,
		Verdict:   preflight.VerdictReady,
		Arguments: &PreflightArguments{
			ToolIDs: []string{" gh:create_issue ", "gh:create_issue", "slack:post"},
			Filters: []string{"read_only_only"},
		},
		Tools: []PreflightToolOutcome{
			{ID: "gh:create_issue", Status: preflight.StatusReady},
			{ID: "slack:post", Status: preflight.StatusReady},
		},
	}))

	filter := storage.DefaultActivityFilter()
	filter.RequestID = "req-args"
	records, _, err := mgr.ListActivities(filter)
	require.NoError(t, err)
	require.Len(t, records, 1)
	metadata := records[0].Metadata

	assert.InDelta(t, 2, metadata[storage.MetadataKeyPreflightIDsCount], 0.0001,
		"ids_count stays the UNIQUE count, as on the REST surface")

	arguments, ok := metadata[storage.MetadataKeyPreflightArguments].(map[string]interface{})
	require.True(t, ok, "the arguments must survive the BBolt JSON round trip")
	ids, ok := arguments[storage.PreflightArgumentsKeyToolIDs].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{" gh:create_issue ", "gh:create_issue", "slack:post"}, ids,
		"as sent: request order, untrimmed, duplicates intact")
	assert.Len(t, ids, 3, "the raw requested count is recoverable")
	assert.Equal(t, []interface{}{"read_only_only"}, arguments[storage.PreflightArgumentsKeyFilters])
}

// The REST surface sets neither Surface nor Arguments, and its metadata must
// therefore carry exactly the four keys spec 098 shipped — adding a key to every
// record written since then would change a payload nothing asked to change.
func TestPreflightMetadataRESTKeysAreUnchanged(t *testing.T) {
	metadata := preflightMetadata(PreflightActivity{
		RequestID: "req-rest",
		Verdict:   preflight.VerdictReady,
		Tools:     []PreflightToolOutcome{{ID: "gh:create_issue", Status: preflight.StatusReady}},
	})

	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		storage.MetadataKeyPreflightIDsCount,
		storage.MetadataKeyPreflightPerTool,
		storage.MetadataKeyPreflightReasons,
		storage.MetadataKeyPreflightVerdict,
	}, keys, "the REST record gains no key from a feature it does not use")
}

func TestPreflightActivityStatusMapping(t *testing.T) {
	assert.Equal(t, storage.ActivityStatusSuccess, PreflightActivityStatus(preflight.VerdictReady))
	for _, verdict := range []string{
		preflight.VerdictDegradedRetryable,
		preflight.VerdictBlocked,
		preflight.VerdictUnknownIDs,
	} {
		assert.Equal(t, storage.ActivityStatusBlocked, PreflightActivityStatus(verdict), verdict)
	}
}
