package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// Spec 109 FR-028 / T014: GET /tools and GET /servers/{id}/tools rows carry a
// `tier` field computed by contracts.AnnotationTier — the one place this is
// computed, so the Web/macOS/CLI surfaces never derive their own (X11).

func TestGlobalTools_RowsCarryTier(t *testing.T) {
	ctrl := &globalToolsController{
		allServers: []map[string]interface{}{{"name": "fs"}},
		serverTools: map[string][]map[string]interface{}{
			"fs": {
				{"name": "delete_file", "description": "d", "annotations": map[string]interface{}{"destructiveHint": true}},
				{"name": "write_file", "description": "d", "annotations": map[string]interface{}{"readOnlyHint": false}},
				{"name": "read_file", "description": "d", "annotations": map[string]interface{}{"readOnlyHint": true}},
				{"name": "mystery_tool", "description": "d"}, // no annotations at all
			},
		},
	}

	data := doGlobalTools(t, ctrl)
	tools := data["tools"].([]interface{})
	require.Len(t, tools, 4)

	byName := map[string]map[string]interface{}{}
	for _, x := range tools {
		tm := x.(map[string]interface{})
		byName[tm["name"].(string)] = tm
	}

	assert.Equal(t, "destructive", byName["delete_file"]["tier"])
	assert.Equal(t, "write", byName["write_file"]["tier"])
	assert.Equal(t, "read", byName["read_file"]["tier"])
	assert.Equal(t, "unannotated", byName["mystery_tool"]["tier"])
}

func TestServerTools_RowsCarryTier(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	// scopeFixtureServers' tools carry no annotations, so this exercises the
	// "unannotated" branch through the real GET /servers/{id}/tools handler.
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/alpha/tools", http.NoBody)
	req.Header.Set("X-API-Key", scopeAdminAPIKey)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok)
	tools, ok := data["tools"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, tools)
	for _, x := range tools {
		tm := x.(map[string]interface{})
		assert.Contains(t, tm, "tier", "every row must carry tier (FR-028)")
	}
}
