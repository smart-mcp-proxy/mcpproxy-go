package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// annotationCoverageController extends MockServerController with customizable tool responses
type annotationCoverageController struct {
	MockServerController
	allServers  []map[string]interface{}
	serverTools map[string][]map[string]interface{}
}

func (m *annotationCoverageController) GetAllServers() ([]map[string]interface{}, error) {
	return m.allServers, nil
}

func (m *annotationCoverageController) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	tools, ok := m.serverTools[serverName]
	if !ok {
		return []map[string]interface{}{}, nil
	}
	return tools, nil
}

func TestHandleAnnotationCoverage(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	ctrl := &annotationCoverageController{
		allServers: []map[string]interface{}{
			{"name": "github-server"},
			{"name": "slack-server"},
		},
		serverTools: map[string][]map[string]interface{}{
			"github-server": {
				{
					"name":        "create_issue",
					"description": "Create a GitHub issue",
					"annotations": &config.ToolAnnotations{
						ReadOnlyHint:    boolPtr(false),
						DestructiveHint: boolPtr(false),
					},
				},
				{
					"name":        "list_repos",
					"description": "List repositories",
					"annotations": &config.ToolAnnotations{
						ReadOnlyHint: boolPtr(true),
					},
				},
				{
					"name":        "get_user",
					"description": "Get user info",
					// No annotations
				},
			},
			"slack-server": {
				{
					"name":        "send_message",
					"description": "Send a Slack message",
					"annotations": &config.ToolAnnotations{
						DestructiveHint: boolPtr(false),
						OpenWorldHint:   boolPtr(true),
					},
				},
				{
					"name":        "list_channels",
					"description": "List channels",
					// No annotations
				},
			},
		},
	}

	srv := NewServer(ctrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/annotations/coverage", nil)
	// Add API key bypass by setting request source to socket (trusted)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()

	srv.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok, "expected data field in response")

	// Total: 5 tools, 3 annotated (create_issue, list_repos, send_message)
	assert.Equal(t, float64(5), data["total_tools"])
	assert.Equal(t, float64(3), data["annotated_tools"])
	assert.Equal(t, float64(60), data["coverage_percent"])

	servers, ok := data["servers"].([]interface{})
	require.True(t, ok)
	assert.Len(t, servers, 2)

	// Find each server in the response (order may vary)
	serverMap := make(map[string]map[string]interface{})
	for _, s := range servers {
		srv := s.(map[string]interface{})
		serverMap[srv["name"].(string)] = srv
	}

	github := serverMap["github-server"]
	require.NotNil(t, github)
	assert.Equal(t, float64(3), github["total_tools"])
	assert.Equal(t, float64(2), github["annotated_tools"])
	// 2/3 = 66.66... rounded to 66.67
	assert.InDelta(t, 66.67, github["coverage_percent"], 0.01)

	slack := serverMap["slack-server"]
	require.NotNil(t, slack)
	assert.Equal(t, float64(2), slack["total_tools"])
	assert.Equal(t, float64(1), slack["annotated_tools"])
	assert.Equal(t, float64(50), slack["coverage_percent"])
}

func TestAnnotationCoverage_EmptyServers(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	ctrl := &annotationCoverageController{
		allServers:  []map[string]interface{}{},
		serverTools: map[string][]map[string]interface{}{},
	}

	srv := NewServer(ctrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/annotations/coverage", nil)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()

	srv.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(0), data["total_tools"])
	assert.Equal(t, float64(0), data["annotated_tools"])
	assert.Equal(t, float64(0), data["coverage_percent"])

	servers, ok := data["servers"].([]interface{})
	require.True(t, ok)
	assert.Len(t, servers, 0)
}

func TestAnnotationCoverage_TitleOnlyNotCounted(t *testing.T) {
	// A tool with only Title set (no hint booleans) should NOT count as annotated
	logger := zaptest.NewLogger(t).Sugar()

	ctrl := &annotationCoverageController{
		allServers: []map[string]interface{}{
			{"name": "test-server"},
		},
		serverTools: map[string][]map[string]interface{}{
			"test-server": {
				{
					"name":        "tool_with_title_only",
					"description": "Has annotations but only title",
					"annotations": &config.ToolAnnotations{
						Title: "My Tool",
					},
				},
				{
					"name":        "tool_with_hints",
					"description": "Has actual hint annotations",
					"annotations": &config.ToolAnnotations{
						Title:        "Another Tool",
						ReadOnlyHint: boolPtr(true),
					},
				},
			},
		},
	}

	srv := NewServer(ctrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/annotations/coverage", nil)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()

	srv.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(2), data["total_tools"])
	assert.Equal(t, float64(1), data["annotated_tools"])
	assert.Equal(t, float64(50), data["coverage_percent"])
}
