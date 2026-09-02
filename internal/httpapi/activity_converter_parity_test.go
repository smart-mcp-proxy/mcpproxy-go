package httpapi

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// There are two storage -> contracts converters and they drifted.
//
// storageToContractActivity feeds GET /api/v1/activity and
// GET /api/v1/activity/{id}; storageToContractActivityForExport feeds the
// NDJSON/CSV export. The export converter copied RequestBytes/ResponseBytes and
// the list converter did not, so the same record answered
// `response_storage_truncated: true` with NO `response_bytes` on the list
// endpoint and `response_bytes: 200039` in the export — and response_bytes is
// precisely the number a client needs to know how much of the body was cut.
//
// The guard below is written as PARITY rather than as "the list converter
// copies response_bytes" on purpose: the failure mode is drift, and a
// field-by-field assertion catches the NEXT field one converter gains and the
// other does not, without anyone remembering to extend this file.

// fullyPopulatedStorageRecord sets every field the converters can carry to a
// distinct non-zero value, so a field a converter silently drops shows up as a
// zero on one side of the comparison.
func fullyPopulatedStorageRecord() *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID:                       "01JABCDEF0000000000000000",
		Type:                     storage.ActivityTypeToolCall,
		Source:                   storage.ActivitySourceMCP,
		ServerName:               "github",
		ToolName:                 "list_issues",
		Arguments:                map[string]interface{}{"repo": "smart-mcp-proxy/mcpproxy-go"},
		Response:                 "only the first 64KB of a much longer payload...[truncated]",
		ResponseTruncated:        true,
		ResponseTruncationCut:    contracts.CutShortenedAgentAndRecord,
		ResponseStorageTruncated: true,
		Status:                   "success",
		ErrorMessage:             "none",
		DurationMs:               1234,
		Timestamp:                time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		SessionID:                "sess-1",
		WorkSessionID:            "ws-1",
		RequestID:                "req-1",
		ParentID:                 "parent-1",
		RequestBytes:             4096,
		ResponseBytes:            200039,
		Metadata: map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected":        true,
				"detection_count": 1,
				"detections": []interface{}{
					map[string]interface{}{"type": "aws_access_key", "severity": "critical"},
				},
			},
		},
	}
}

func TestActivityConvertersAgreeFieldByField(t *testing.T) {
	rec := fullyPopulatedStorageRecord()

	list := storageToContractActivity(rec)
	// includeBodies=true is the apples-to-apples comparison: the bodies are the
	// ONLY thing the export is allowed to withhold, and withholding them is a
	// caller choice, not a converter difference.
	export := storageToContractActivityForExport(rec, true)

	lv, ev := reflect.ValueOf(list), reflect.ValueOf(export)
	typ := lv.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		assert.True(t, reflect.DeepEqual(lv.Field(i).Interface(), ev.Field(i).Interface()),
			"converters disagree on %s: list=%v export=%v", f.Name,
			lv.Field(i).Interface(), ev.Field(i).Interface())
	}
}

// The parity test above only bites if the fixture actually exercises every
// field: a field left at its zero value would compare equal on both sides
// however badly it were dropped. This asserts the fixture is complete, so
// adding a field to contracts.ActivityRecord without extending the fixture
// fails here rather than creating a silent hole in the guard above.
func TestActivityConverterParityFixtureLeavesNoFieldZero(t *testing.T) {
	record := storageToContractActivityForExport(fullyPopulatedStorageRecord(), true)

	v := reflect.ValueOf(record)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		assert.False(t, v.Field(i).IsZero(),
			"%s is zero in the parity fixture, so the parity guard cannot see it drop",
			typ.Field(i).Name)
	}
}

// The specific regression, stated plainly so a failure names the bug rather
// than a reflection index.
func TestListConverterCarriesPreTruncationByteCounts(t *testing.T) {
	rec := fullyPopulatedStorageRecord()

	got := storageToContractActivity(rec)

	require.True(t, got.ResponseStorageTruncated,
		"precondition: this record's stored body is a prefix")
	assert.Equal(t, 200039, got.ResponseBytes,
		"a client told the body was cut needs the pre-truncation size to know by how much")
	assert.Equal(t, 4096, got.RequestBytes)
}

// Guard against the opposite over-correction: the byte counts are sizes, not
// content, so a bodies-off export must still carry them (spec 103 relies on it)
// while the bodies themselves stay withheld.
func TestExportWithoutBodiesStillCarriesByteCounts(t *testing.T) {
	got := storageToContractActivityForExport(fullyPopulatedStorageRecord(), false)

	assert.Empty(t, got.Response)
	assert.Empty(t, got.Arguments)
	assert.Equal(t, 200039, got.ResponseBytes)
	assert.Equal(t, 4096, got.RequestBytes)
	assert.True(t, got.ResponseStorageTruncated)
}
