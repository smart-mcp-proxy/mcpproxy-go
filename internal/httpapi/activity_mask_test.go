package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The activity drawer used to print the very credential the detector had just
// flagged, in cleartext, next to a Copy button (UX audit F13). The fix has to
// hold on the SERVER: redacting in the browser would leave the raw value on the
// wire and in the network log.

const maskTestSecret = "AKIAIOSFODNN7EXAMPLE"

func flaggedActivityRecord() *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID:         "activity-flagged",
		Type:       storage.ActivityTypeToolCall,
		ServerName: "everything",
		ToolName:   "echo",
		Status:     "success",
		Timestamp:  time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
		Arguments: map[string]interface{}{
			"message":         maskTestSecret + " aws key here",
			"_auth_auth_type": "admin",
			"_auth_user_id":   "u-1",
		},
		Response: "Echo: " + maskTestSecret + " aws key here",
		Metadata: map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected": true,
				"detections": []interface{}{
					map[string]interface{}{
						"type":     "aws_access_key",
						"severity": "critical",
						"location": "arguments",
					},
				},
			},
		},
	}
}

func newMaskingServer(t *testing.T, records ...*storage.ActivityRecord) *Server {
	t.Helper()
	srv := NewServer(&mockActivityController{apiKey: "test-key", activities: records}, zap.NewNop().Sugar(), nil)
	srv.SetSensitiveMasker(security.NewDetector(nil))
	return srv
}

func getJSON(t *testing.T, srv *Server, target string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok, "unexpected data shape: %T", resp.Data)
	return data
}

func TestActivityDetail_MasksFlaggedSecret(t *testing.T) {
	srv := newMaskingServer(t, flaggedActivityRecord())

	data := getJSON(t, srv, "/api/v1/activity/activity-flagged")
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body := string(raw)

	assert.NotContains(t, body, maskTestSecret, "detail response served the flagged secret verbatim")
	assert.Contains(t, body, "AKIA…****", "masked preview missing — the operator cannot tell which key leaked")
	assert.Contains(t, body, "aws key here", "masking swallowed the surrounding payload")

	activity := data["activity"].(map[string]interface{})
	args := activity["arguments"].(map[string]interface{})
	assert.NotContains(t, args, "_auth_auth_type", "internal auth plumbing leaked into the payload view")
	assert.NotContains(t, args, "_auth_user_id")
}

func TestActivityList_MasksFlaggedSecret(t *testing.T) {
	srv := newMaskingServer(t, flaggedActivityRecord())

	data := getJSON(t, srv, "/api/v1/activity")
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body := string(raw)

	assert.NotContains(t, body, maskTestSecret, "list response served the flagged secret verbatim")
	assert.Contains(t, body, "AKIA…****")
}

func TestActivityList_LeavesCleanRecordsAlone(t *testing.T) {
	clean := &storage.ActivityRecord{
		ID:         "activity-clean",
		Type:       storage.ActivityTypeToolCall,
		ServerName: "everything",
		ToolName:   "echo",
		Status:     "success",
		Timestamp:  time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
		Arguments:  map[string]interface{}{"message": "hello world"},
		Response:   "Echo: hello world",
	}
	srv := newMaskingServer(t, clean)

	data := getJSON(t, srv, "/api/v1/activity")
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body := string(raw)

	assert.Contains(t, body, "hello world")
	assert.NotContains(t, body, "****")
}

// An upstream error commonly quotes the payload it choked on, so masking the
// response and not the error just moves the leak.
func TestActivityDetail_MasksErrorMessage(t *testing.T) {
	record := flaggedActivityRecord()
	record.Status = "error"
	record.Response = ""
	record.ErrorMessage = "upstream rejected credentials: " + maskTestSecret

	srv := newMaskingServer(t, record)

	data := getJSON(t, srv, "/api/v1/activity/activity-flagged")
	raw, err := json.Marshal(data)
	require.NoError(t, err)

	assert.NotContains(t, string(raw), maskTestSecret, "error message served the flagged secret verbatim")
	assert.Contains(t, string(raw), "AKIA…****")
	assert.Contains(t, string(raw), "upstream rejected credentials")
}

// SSE events are emitted at completion time, BEFORE the async detector has a
// verdict, so this path masks unconditionally.
func TestMaskEventPayload_MasksActivityEvent(t *testing.T) {
	srv := newMaskingServer(t)

	masked := srv.maskEventPayload(map[string]interface{}{
		"server_name": "everything",
		"tool_name":   "echo",
		"arguments": map[string]interface{}{
			"message":       maskTestSecret,
			"_auth_user_id": "u-1",
		},
		"response": "Echo: " + maskTestSecret,
		"error":    "failed on " + maskTestSecret,
	})

	args := masked["arguments"].(map[string]interface{})
	assert.NotContains(t, args["message"], maskTestSecret)
	assert.NotContains(t, args, "_auth_user_id", "internal auth plumbing streamed to SSE subscribers")
	assert.NotContains(t, masked["response"], maskTestSecret)
	assert.NotContains(t, masked["error"], maskTestSecret)
	assert.Equal(t, "echo", masked["tool_name"], "non-payload fields must survive untouched")
}

func TestMaskEventPayload_LeavesNonPayloadEventsAlone(t *testing.T) {
	srv := newMaskingServer(t)

	payload := map[string]interface{}{"server_name": "everything", "state": "ready"}
	masked := srv.maskEventPayload(payload)

	assert.Equal(t, payload, masked)
}

// The legacy tool-call store carries no detection verdict, so its endpoints
// mask unconditionally.
func TestMaskToolCallRecord(t *testing.T) {
	srv := newMaskingServer(t)

	record := contracts.ToolCallRecord{
		ToolName:  "echo",
		Arguments: map[string]interface{}{"message": maskTestSecret, "_auth_auth_type": "admin"},
		Response:  map[string]interface{}{"text": "Echo: " + maskTestSecret},
		Error:     "boom " + maskTestSecret,
	}

	srv.maskToolCallRecord(&record)

	raw, err := json.Marshal(record)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), maskTestSecret)
	assert.NotContains(t, string(raw), "_auth_auth_type")
	assert.Contains(t, string(raw), "AKIA…****")
}

// The compliance export is the one deliberate full-value surface: it carries no
// payload at all unless the caller asks for include_bodies=true, and it is an
// incident-response tool rather than a browsing view.
func TestActivityExport_BodiesGatedOnExplicitFlag(t *testing.T) {
	record := flaggedActivityRecord()

	withoutBodies := storageToContractActivityForExport(record, false)
	assert.Empty(t, withoutBodies.Response, "default export must not carry the response body")
	assert.Nil(t, withoutBodies.Arguments, "default export must not carry arguments")

	withBodies := storageToContractActivityForExport(record, true)
	assert.True(t, strings.Contains(withBodies.Response, maskTestSecret),
		"the explicit compliance export should still resolve full values")
}
