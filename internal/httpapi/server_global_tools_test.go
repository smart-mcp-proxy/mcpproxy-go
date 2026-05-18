package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// globalToolsController drives the GET /api/v1/tools handler (spec 050).
type globalToolsController struct {
	MockServerController
	allServers   []map[string]interface{}
	serverTools  map[string][]map[string]interface{}
	serverErr    map[string]error
	approvals    map[string]*storage.ToolApprovalRecord // key serverName + "\x00" + toolName
	configDenied map[string]bool
	usage        map[string]storage.ToolUsageStat
	usageErr     error
}

// GetManagementService returns nil so handleGetGlobalTools exercises the
// controller GetServerTools path this mock controls. The management-service
// path is verified end-to-end via the API E2E + curl verification.
func (m *globalToolsController) GetManagementService() interface{} { return nil }

func (m *globalToolsController) GetAllServers() ([]map[string]interface{}, error) {
	return m.allServers, nil
}

func (m *globalToolsController) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	if err, ok := m.serverErr[serverName]; ok {
		return nil, err
	}
	return m.serverTools[serverName], nil
}

func (m *globalToolsController) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	if r, ok := m.approvals[serverName+"\x00"+toolName]; ok {
		return r, nil
	}
	return nil, nil
}

func (m *globalToolsController) IsToolConfigDenied(serverName, toolName string) bool {
	return m.configDenied[serverName+"\x00"+toolName]
}

func (m *globalToolsController) AggregateToolUsage(_ time.Time) (map[string]storage.ToolUsageStat, error) {
	if m.usageErr != nil {
		return nil, m.usageErr
	}
	return m.usage, nil
}

func doGlobalTools(t *testing.T, ctrl *globalToolsController) map[string]interface{} {
	t.Helper()
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest("GET", "/api/v1/tools", nil)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok, "expected data object")
	return data
}

func TestGlobalTools_MergeEnrichmentStatsUsage(t *testing.T) {
	lastUsed := time.Now().Add(-2 * time.Hour).UTC()
	ctrl := &globalToolsController{
		allServers: []map[string]interface{}{
			{"name": "github"},
			{"name": "memory"}, // disabled server — tools must still appear
		},
		serverTools: map[string][]map[string]interface{}{
			"github": {
				{"name": "create_issue", "description": "Create issue"},
				{"name": "delete_repo", "description": "Delete repo"},
			},
			"memory": {
				{"name": "store", "description": "Store memory"},
			},
		},
		approvals: map[string]*storage.ToolApprovalRecord{
			"github\x00create_issue": {Status: storage.ToolApprovalStatusApproved, Disabled: true},
			"github\x00delete_repo":  {Status: storage.ToolApprovalStatusPending},
		},
		configDenied: map[string]bool{"memory\x00store": true},
		usage: map[string]storage.ToolUsageStat{
			"github\x00create_issue": {Count: 7, LastUsed: lastUsed},
		},
	}

	data := doGlobalTools(t, ctrl)

	tools := data["tools"].([]interface{})
	assert.Len(t, tools, 3, "all tools from all servers incl. disabled server")

	byName := map[string]map[string]interface{}{}
	for _, x := range tools {
		tm := x.(map[string]interface{})
		byName[tm["name"].(string)] = tm
	}
	// disabled-server tool present and attributed
	require.Contains(t, byName, "store")
	assert.Equal(t, "memory", byName["store"]["server_name"])
	assert.Equal(t, true, byName["store"]["config_denied"])
	// enrichment + usage
	assert.Equal(t, true, byName["create_issue"]["disabled"])
	assert.Equal(t, float64(7), byName["create_issue"]["usage"])
	assert.NotEmpty(t, byName["create_issue"]["last_used"])
	// never-used tool: usage 0, no last_used
	assert.Equal(t, float64(0), byName["delete_repo"]["usage"])
	assert.NotContains(t, byName["delete_repo"], "last_used")

	stats := data["stats"].(map[string]interface{})
	assert.Equal(t, float64(3), stats["total"])
	// create_issue (user-disabled) + store (config-denied) => 2 disabled
	assert.Equal(t, float64(2), stats["disabled"])
	assert.Equal(t, float64(1), stats["enabled"])
	assert.Equal(t, float64(1), stats["pending_approval"]) // delete_repo pending
	assert.NotEqual(t, true, data["partial"])
}

func TestGlobalTools_PartialServerFailureDoesNotFail(t *testing.T) {
	ctrl := &globalToolsController{
		allServers: []map[string]interface{}{
			{"name": "ok"},
			{"name": "broken"},
		},
		serverTools: map[string][]map[string]interface{}{
			"ok": {{"name": "ping", "description": "ping"}},
		},
		serverErr: map[string]error{"broken": errors.New("connection refused")},
	}

	data := doGlobalTools(t, ctrl)

	assert.Len(t, data["tools"].([]interface{}), 1)
	assert.Equal(t, true, data["partial"])
	failed := data["failed_servers"].([]interface{})
	assert.Equal(t, []interface{}{"broken"}, failed)
}

func TestGlobalTools_UsageErrorDegradesGracefully(t *testing.T) {
	ctrl := &globalToolsController{
		allServers:  []map[string]interface{}{{"name": "s"}},
		serverTools: map[string][]map[string]interface{}{"s": {{"name": "t", "description": "d"}}},
		usageErr:    errors.New("bolt closed"),
	}
	data := doGlobalTools(t, ctrl)
	tools := data["tools"].([]interface{})
	require.Len(t, tools, 1)
	assert.Equal(t, float64(0), tools[0].(map[string]interface{})["usage"])
}

func TestGlobalTools_Empty(t *testing.T) {
	ctrl := &globalToolsController{allServers: []map[string]interface{}{}}
	data := doGlobalTools(t, ctrl)
	assert.Len(t, data["tools"].([]interface{}), 0)
	assert.Equal(t, float64(0), data["stats"].(map[string]interface{})["total"])
}
