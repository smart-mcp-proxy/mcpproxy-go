package storage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 113-c FR-043: a record written before the call-error taxonomy decodes
// unchanged and re-encodes byte-identically (no new keys appear).
func TestActivityRecordPreSpec113JSONRoundTripsByteIdentical(t *testing.T) {
	legacy := `{"id":"01HZ","type":"tool_call","source":"mcp","server_name":"srv","tool_name":"t","status":"error","error_message":"boom","duration_ms":12,"timestamp":"2026-01-02T03:04:05Z","request_id":"r1"}`

	var rec ActivityRecord
	require.NoError(t, json.Unmarshal([]byte(legacy), &rec))
	assert.Empty(t, rec.ErrorClass)
	assert.Empty(t, rec.FaultDomain)
	assert.Zero(t, rec.UpstreamHTTPStatus)

	out, err := json.Marshal(&rec)
	require.NoError(t, err)
	assert.JSONEq(t, legacy, string(out))
	assert.NotContains(t, string(out), "error_class")
	assert.NotContains(t, string(out), "fault_domain")
	assert.NotContains(t, string(out), "upstream_http_status")
}

func TestActivityRecordTaxonomyFieldsRoundTrip(t *testing.T) {
	rec := ActivityRecord{ID: "x", Type: ActivityTypeToolCall, Status: "error",
		ErrorClass: "http", FaultDomain: "upstream", UpstreamHTTPStatus: 502}
	data, err := rec.MarshalBinary()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"error_class":"http"`)
	assert.Contains(t, string(data), `"upstream_http_status":502`)

	var back ActivityRecord
	require.NoError(t, back.UnmarshalBinary(data))
	assert.Equal(t, rec.ErrorClass, back.ErrorClass)
	assert.Equal(t, rec.FaultDomain, back.FaultDomain)
	assert.Equal(t, 502, back.UpstreamHTTPStatus)
}

// A success record carries none of the fields.
func TestActivityRecordSuccessOmitsTaxonomy(t *testing.T) {
	data, err := json.Marshal(&ActivityRecord{ID: "x", Type: ActivityTypeToolCall, Status: ActivityStatusSuccess})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "error_class")
}
