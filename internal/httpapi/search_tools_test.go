package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// canonicalSearchController mimics the post-#871 index projection: the
// controller's SearchTools now hands back the canonical "server:tool" id in the
// tool's name field (the index read seams reattach the server prefix for the
// MCP discovery surface). The REST /api/v1/index/search response must not leak
// that prefix — external consumers (the D1 retrieval-regression scorer) build
// the id themselves from server_name + name, so a prefixed name double-prefixes.
type canonicalSearchController struct {
	*MockServerController
}

func (c *canonicalSearchController) SearchTools(_ string, _ int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"tool": map[string]interface{}{
				"name":        "github:create_issue",
				"server_name": "github",
				"description": "Create a new issue",
			},
			"score": 0.9,
		},
	}, nil
}

// TestSearchTools_RestNameIsBare pins the REST index-search contract (#871):
// an indexed {ServerName:"github", Name:"create_issue"} must surface over
// /api/v1/index/search as name "create_issue" with server_name "github", even
// though the index read seams now canonicalize the name to "github:create_issue"
// for the MCP surface. This is the regression guard for the D1 gate breakage.
func TestSearchTools_RestNameIsBare(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	server := NewServer(&canonicalSearchController{&MockServerController{}}, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/search?q=issue", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var response contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)

	payload, err := json.Marshal(response.Data)
	require.NoError(t, err)
	var typed contracts.SearchToolsResponse
	require.NoError(t, json.Unmarshal(payload, &typed))

	require.Len(t, typed.Results, 1)
	tool := typed.Results[0].Tool
	assert.Equal(t, "create_issue", tool.Name, "REST search must return the bare tool name, not the canonical server:tool id")
	assert.Equal(t, "github", tool.ServerName, "REST search must keep server_name populated so consumers can rebuild the id")
}
