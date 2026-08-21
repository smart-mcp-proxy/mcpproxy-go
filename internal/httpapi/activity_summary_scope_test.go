package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// summaryScopeController serves the same record set through both the paginated
// list path and the streaming path, so a summary computed over either one is
// comparable.
type summaryScopeController struct {
	baseController
	activities []*storage.ActivityRecord
}

func (m *summaryScopeController) GetCurrentConfig() any {
	return &config.Config{APIKey: "test-key"}
}

func (m *summaryScopeController) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	// storage.Manager.ListActivities normalises the filter before it reads
	// anything, and that normalisation is the whole bug: Validate() turns limit
	// 0 into 50 and caps it at 100. A mock that skipped it would let the
	// regression pass unnoticed.
	filter.Validate()

	var matched []*storage.ActivityRecord
	for _, a := range m.activities {
		if filter.Matches(a) {
			matched = append(matched, a)
		}
	}
	total := len(matched)
	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	return matched, total, nil
}

func (m *summaryScopeController) StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord {
	ch := make(chan *storage.ActivityRecord)
	go func() {
		defer close(ch)
		sent := 0
		for _, a := range m.activities {
			if !filter.Matches(a) {
				continue
			}
			if filter.Limit > 0 && sent >= filter.Limit {
				return
			}
			ch <- a
			sent++
		}
	}()
	return ch
}

// GET /api/v1/activity/summary answers a question about a PERIOD ("24h"), so it
// has to see the whole period. It used to run through the paginated list path,
// whose Validate() turns limit 0 into 50 and caps it at 100 — so on any proxy
// busier than a demo, the totals and the status split described the newest 50
// records while claiming to describe 24 hours.
func TestActivitySummary_CountsEveryRecordInThePeriodNotJustAPage(t *testing.T) {
	const (
		successes = 120
		errors    = 40
		total     = successes + errors
	)
	now := time.Now().UTC()

	var activities []*storage.ActivityRecord
	// Newest first, matching the storage cursor's order: the errors are OLDER
	// than a 50-record page, so a paginated summary misses all of them.
	for i := 0; i < successes; i++ {
		activities = append(activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("ok-%d", i), Type: storage.ActivityTypeToolCall,
			ServerName: "github", ToolName: "search",
			Status: storage.ActivityStatusSuccess, Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < errors; i++ {
		activities = append(activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("err-%d", i), Type: storage.ActivityTypeToolCall,
			ServerName: "gitlab", ToolName: "list",
			Status: storage.ActivityStatusError, Timestamp: now.Add(-time.Duration(successes+i) * time.Minute),
		})
	}

	srv := NewServer(&summaryScopeController{activities: activities}, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/summary?period=24h", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                              `json:"success"`
		Data    contracts.ActivitySummaryResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.Success)

	assert.Equal(t, total, resp.Data.TotalCount, "the summary covers the period, not the first page")
	assert.Equal(t, successes, resp.Data.SuccessCount)
	assert.Equal(t, errors, resp.Data.ErrorCount, "the older errors must not fall off the end of a page")
	require.NotEmpty(t, resp.Data.TopServers)
	assert.Equal(t, "github", resp.Data.TopServers[0].Name)
	require.Len(t, resp.Data.TopServers, 2, "both servers are in range, not only the newest page's")
}
