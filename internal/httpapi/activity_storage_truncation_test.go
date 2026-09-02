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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// storageTruncationController serves one record through both the list and the
// export path, so these tests exercise the real handlers rather than restating
// the converters' own code.
type storageTruncationController struct {
	baseController
	activities []*storage.ActivityRecord
}

func (m *storageTruncationController) GetCurrentConfig() any {
	return &config.Config{APIKey: "test-key"}
}

func (m *storageTruncationController) ListActivities(_ storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	return m.activities, len(m.activities), nil
}

func (m *storageTruncationController) StreamActivities(_ storage.ActivityFilter) <-chan *storage.ActivityRecord {
	ch := make(chan *storage.ActivityRecord)
	go func() {
		defer close(ch)
		for _, a := range m.activities {
			ch <- a
		}
	}()
	return ch
}

func storageTruncatedRecord() *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID:                       "01ABCDEFGHIJKLMNOPQRSTUVWX",
		Type:                     storage.ActivityTypeInternalToolCall,
		Source:                   storage.ActivitySourceMCP,
		ToolName:                 "call_tool_read",
		Status:                   storage.ActivityStatusSuccess,
		Timestamp:                time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		Response:                 "a prefix...[truncated]",
		ResponseBytes:            200_000,
		ResponseStorageTruncated: true,
	}
}

func storageTruncationServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(&storageTruncationController{
		activities: []*storage.ActivityRecord{storageTruncatedRecord()},
	}, zap.NewNop().Sugar(), nil)
}

func getActivityRecord(t *testing.T, srv *Server, path string) contracts.ActivityRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data contracts.ActivityListResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Data.Activities, 1)
	return resp.Data.Activities[0]
}

// The list projection has to carry the flag, or a consumer cannot tell a body
// the log cut from a complete one — the whole reason the field exists on the
// wire and not only in BBolt.
func TestActivityListCarriesStorageTruncation(t *testing.T) {
	got := getActivityRecord(t, storageTruncationServer(t), "/api/v1/activity")

	assert.True(t, got.ResponseStorageTruncated)
	assert.False(t, got.ResponseTruncated,
		"the Spec 103 flag means the opposite direction and must stay independent")
}

// exclude_payloads suppresses the body, so it must also suppress the claim that
// the body was cut: a projection carrying no response at all must not assert
// anything about one.
func TestActivityListExcludePayloadsClearsStorageTruncation(t *testing.T) {
	got := getActivityRecord(t, storageTruncationServer(t), "/api/v1/activity?exclude_payloads=true")

	require.Empty(t, got.Response, "premise: the body really is suppressed")
	assert.False(t, got.ResponseStorageTruncated,
		"a payload-free projection must not claim its absent payload was truncated")
}

// The CSV export is positional by contract — activity.go's own header comment
// says a new column has to land after the last one, because consumers index by
// position. Driving the real endpoint is what makes the header and the row
// provably agree; asserting a literal in the test would only restate it.
func TestActivityCSVExportAppendsStorageTruncationColumn(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/export?format=csv", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	storageTruncationServer(t).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	lines := strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n")
	require.Len(t, lines, 2, "one header plus one record")

	cols := strings.Split(lines[0], ",")
	fields := strings.Split(lines[1], ",")
	require.Len(t, fields, len(cols), "row width must match header width")

	require.Equal(t, "parent_id", cols[len(cols)-2],
		"parent_id must keep its column index; a new column is appended after it")
	require.Equal(t, "response_storage_truncated", cols[len(cols)-1])

	idx := func(name string) int {
		for i, c := range cols {
			if c == name {
				return i
			}
		}
		t.Fatalf("column %q missing from CSV header", name)
		return -1
	}
	assert.Equal(t, "true", fields[idx("response_storage_truncated")])
	assert.Equal(t, "false", fields[idx("response_truncated")],
		"the two columns describe opposite directions and must not report one bit")
}

// The export converter is a separate code path from the list converter and has
// its own bodies gate. The flag describes the record rather than its content,
// so it must survive a bodies-off export — the posture in which byte accounting
// is the only cost signal a consumer has.
func TestExportConverterCarriesStorageTruncationWithBodiesOff(t *testing.T) {
	got := storageToContractActivityForExport(storageTruncatedRecord(), false)

	require.Empty(t, got.Response, "premise: bodies really are off")
	assert.True(t, got.ResponseStorageTruncated)
	assert.False(t, got.ResponseTruncated)
}
