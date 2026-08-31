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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Cross-model review of #1146, finding 3. Giving the management built-ins a
// target_server fixed the Activity Log's Server column — and, as a side effect,
// let their rows into /api/v1/activity/summary's per-server aggregation, which
// previously skipped them only by the accident of an empty ServerName.
//
// top_servers / top_tools answer "which upstreams is this proxy talking to".
// upstream_servers and quarantine_security are mcpproxy's own housekeeping —
// the population storage.CountsAsCall already excludes by name — so a burst of
// config edits must not read as traffic to the server being configured, and
// "github:upstream_servers" is not a tool github owns.
func TestActivitySummary_ManagementChatterIsNotUpstreamTraffic(t *testing.T) {
	const (
		realCalls       = 3
		managementRows  = 10
		managementTool  = "upstream_servers"
		quarantineTool  = "quarantine_security"
		quarantineCount = 4
	)
	now := time.Now().UTC()

	var activities []*storage.ActivityRecord
	for i := 0; i < realCalls; i++ {
		activities = append(activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("call-%d", i), Type: storage.ActivityTypeToolCall,
			ServerName: "github", ToolName: "search",
			Status: storage.ActivityStatusSuccess, Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < managementRows; i++ {
		activities = append(activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("mgmt-%d", i), Type: storage.ActivityTypeInternalToolCall,
			ServerName: "github", ToolName: managementTool,
			Status: storage.ActivityStatusSuccess, Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < quarantineCount; i++ {
		activities = append(activities, &storage.ActivityRecord{
			ID: fmt.Sprintf("quar-%d", i), Type: storage.ActivityTypeInternalToolCall,
			ServerName: "github", ToolName: quarantineTool,
			Status: storage.ActivityStatusSuccess, Timestamp: now.Add(-time.Duration(i) * time.Minute),
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

	// Every row is still in the log and still in the denominator.
	assert.Equal(t, realCalls+managementRows+quarantineCount, resp.Data.TotalCount,
		"the management rows are real activity and must stay in the total")

	require.Len(t, resp.Data.TopServers, 1)
	assert.Equal(t, "github", resp.Data.TopServers[0].Name)
	assert.Equal(t, realCalls, resp.Data.TopServers[0].Count,
		"config edits to a server are not traffic to it")

	for _, tool := range resp.Data.TopTools {
		assert.NotEqual(t, managementTool, tool.Tool,
			"upstream_servers is mcpproxy's built-in, not a tool the upstream owns")
		assert.NotEqual(t, quarantineTool, tool.Tool,
			"quarantine_security is mcpproxy's built-in, not a tool the upstream owns")
	}
	require.Len(t, resp.Data.TopTools, 1)
	assert.Equal(t, "github", resp.Data.TopTools[0].Server)
	assert.Equal(t, "search", resp.Data.TopTools[0].Tool)
	assert.Equal(t, realCalls, resp.Data.TopTools[0].Count)
}
