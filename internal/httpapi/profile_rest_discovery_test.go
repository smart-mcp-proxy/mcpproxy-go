package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type profileRESTDiscoveryController struct {
	*globalToolsController
	managementService *profileRESTDiscoveryManagementService
	approvals         []*storage.ToolApprovalRecord
}

type profileRESTDiscoveryManagementService struct {
	management.Service
	controller *globalToolsController
}

func (m *profileRESTDiscoveryManagementService) GetServerTools(_ context.Context, server string) ([]map[string]interface{}, error) {
	return m.controller.GetServerTools(server)
}

func (c *profileRESTDiscoveryController) GetManagementService() management.Service {
	return c.managementService
}

func (c *profileRESTDiscoveryController) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	if record, ok := c.globalToolsController.approvals[serverName+"\x00"+toolName]; ok {
		return record, nil
	}
	return nil, fmt.Errorf("%w: %s", storage.ErrToolApprovalNotFound, storage.ToolApprovalKey(serverName, toolName))
}

func (c *profileRESTDiscoveryController) ListToolApprovals(server string) ([]*storage.ToolApprovalRecord, error) {
	rows := make([]*storage.ToolApprovalRecord, 0, len(c.approvals))
	for _, row := range c.approvals {
		if server == "" || row.ServerName == server {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (c *profileRESTDiscoveryController) ToolAllowedByProfile(ctx context.Context, serverName, toolName string) bool {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || ac.IsAdmin() || ac.ProfilePin == "" {
		return true
	}
	return c.globalToolsController.ToolAllowedByProfile(ctx, serverName, toolName)
}

func (c *profileRESTDiscoveryController) SearchToolsForProfile(ctx context.Context, query string, limit int, inScope func(string) bool) ([]map[string]interface{}, bool, error) {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || ac.IsAdmin() || ac.ProfilePin == "" {
		return nil, false, nil
	}
	return c.globalToolsController.SearchToolsForProfile(ctx, query, limit, inScope)
}

func pinnedDiscoveryContext() context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "readonly", ProfilePin: "work-readonly",
		AllowedServers: []string{"github"}, Permissions: []string{auth.PermRead},
	})
}

func profileRouteRequest(caller context.Context, method, path string, params ...[2]string) *http.Request {
	req := httptest.NewRequest(method, path, http.NoBody)
	ctx := chi.NewRouteContext()
	for _, param := range params {
		ctx.URLParams.Add(param[0], param[1])
	}
	return req.WithContext(context.WithValue(caller, chi.RouteCtxKey, ctx))
}

func TestRESTDiscovery_ProfileFiltersServerToolsAndExportAndHidesDiff(t *testing.T) {
	controller := &profileRESTDiscoveryController{
		globalToolsController: &globalToolsController{
			allServers: []map[string]interface{}{{"name": "github", "id": "github"}},
			serverTools: map[string][]map[string]interface{}{
				"github": {
					{"name": "list_issues", "server_name": "github", "description": "List issues"},
					{"name": "create_issue", "server_name": "github", "description": "Create issue"},
				},
			},
			profileAllowed: map[string]bool{"github\x00create_issue": false, "github\x00no_such_tool": true},
			approvals: map[string]*storage.ToolApprovalRecord{
				"github\x00list_issues":  {ServerName: "github", ToolName: "list_issues", Status: storage.ToolApprovalStatusApproved},
				"github\x00create_issue": {ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusChanged},
			},
		},
		approvals: []*storage.ToolApprovalRecord{
			{ServerName: "github", ToolName: "list_issues", Status: storage.ToolApprovalStatusApproved},
			{ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusChanged},
		},
	}
	controller.managementService = &profileRESTDiscoveryManagementService{controller: controller.globalToolsController}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)
	ctx := pinnedDiscoveryContext()

	t.Run("per-server tools", func(t *testing.T) {
		req := profileRouteRequest(ctx, http.MethodGet, "/api/v1/servers/github/tools", [2]string{"id", "github"})
		w := httptest.NewRecorder()
		srv.handleGetServerTools(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var payload struct {
			Data struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Len(t, payload.Data.Tools, 1)
		require.Equal(t, "list_issues", payload.Data.Tools[0].Name)
	})

	t.Run("export", func(t *testing.T) {
		req := profileRouteRequest(ctx, http.MethodGet, "/api/v1/servers/github/tools/export", [2]string{"id", "github"})
		w := httptest.NewRecorder()
		srv.handleExportToolDescriptions(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var payload struct {
			Data struct {
				Count int `json:"count"`
				Tools []struct {
					ToolName string `json:"tool_name"`
				} `json:"tools"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, 1, payload.Data.Count)
		require.Len(t, payload.Data.Tools, 1)
		require.Equal(t, "list_issues", payload.Data.Tools[0].ToolName)
	})

	t.Run("diff for excluded tool is the same not-found shape", func(t *testing.T) {
		req := profileRouteRequest(ctx, http.MethodGet, "/api/v1/servers/github/tools/create_issue/diff",
			[2]string{"id", "github"}, [2]string{"tool", "create_issue"})
		w := httptest.NewRecorder()
		srv.handleGetToolDiff(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
		var hiddenPayload struct {
			Error string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &hiddenPayload))

		unknownReq := profileRouteRequest(ctx, http.MethodGet, "/api/v1/servers/github/tools/no_such_tool/diff",
			[2]string{"id", "github"}, [2]string{"tool", "no_such_tool"})
		unknown := httptest.NewRecorder()
		srv.handleGetToolDiff(unknown, unknownReq)
		require.Equal(t, http.StatusNotFound, unknown.Code)
		var unknownPayload struct {
			Error string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(unknown.Body.Bytes(), &unknownPayload))
		require.Equal(t, unknownPayload.Error, hiddenPayload.Error, "a policy-hidden tool and a nonexistent tool must have identical not-found bodies")
	})

	t.Run("unprofiled administrator keeps the current rows and diff", func(t *testing.T) {
		admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
		toolsReq := profileRouteRequest(admin, http.MethodGet, "/api/v1/servers/github/tools", [2]string{"id", "github"})
		toolsResponse := httptest.NewRecorder()
		srv.handleGetServerTools(toolsResponse, toolsReq)
		require.Equal(t, http.StatusOK, toolsResponse.Code, toolsResponse.Body.String())
		var toolsPayload struct {
			Data struct {
				Count int `json:"count"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(toolsResponse.Body.Bytes(), &toolsPayload))
		require.Equal(t, 2, toolsPayload.Data.Count)

		exportReq := profileRouteRequest(admin, http.MethodGet, "/api/v1/servers/github/tools/export", [2]string{"id", "github"})
		exportResponse := httptest.NewRecorder()
		srv.handleExportToolDescriptions(exportResponse, exportReq)
		require.Equal(t, http.StatusOK, exportResponse.Code, exportResponse.Body.String())
		var exportPayload struct {
			Data struct {
				Count int `json:"count"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(exportResponse.Body.Bytes(), &exportPayload))
		require.Equal(t, 2, exportPayload.Data.Count)

		diffReq := profileRouteRequest(admin, http.MethodGet, "/api/v1/servers/github/tools/create_issue/diff",
			[2]string{"id", "github"}, [2]string{"tool", "create_issue"})
		diffResponse := httptest.NewRecorder()
		srv.handleGetToolDiff(diffResponse, diffReq)
		require.Equal(t, http.StatusOK, diffResponse.Code, diffResponse.Body.String())
	})
}

// StateView tool names are upstream raw identities. In particular, a raw
// name may itself begin with its server name; REST inventory must ask the
// profile policy about that full raw name rather than stripping a prefix.
func TestRESTDiscovery_ProfilePreservesRawPrefixedToolNames(t *testing.T) {
	controller := &profileRESTDiscoveryController{
		globalToolsController: &globalToolsController{
			allServers: []map[string]interface{}{{"name": "github", "id": "github"}},
			serverTools: map[string][]map[string]interface{}{
				"github": {
					{"name": "github:erase", "server_name": "github", "description": "Raw prefixed destructive tool"},
					{"name": "list_issues", "server_name": "github", "description": "Allowed tool"},
				},
			},
			profileAllowed: map[string]bool{
				"github\x00github:erase": false,
			},
		},
	}
	controller.managementService = &profileRESTDiscoveryManagementService{controller: controller.globalToolsController}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)
	ctx := pinnedDiscoveryContext()

	t.Run("per-server tools", func(t *testing.T) {
		req := profileRouteRequest(ctx, http.MethodGet, "/api/v1/servers/github/tools", [2]string{"id", "github"})
		w := httptest.NewRecorder()
		srv.handleGetServerTools(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var payload struct {
			Data struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Len(t, payload.Data.Tools, 1)
		require.Equal(t, "list_issues", payload.Data.Tools[0].Name)
	})

	t.Run("global tools", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", http.NoBody).WithContext(ctx)
		w := httptest.NewRecorder()
		srv.handleGetGlobalTools(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var payload struct {
			Data struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Len(t, payload.Data.Tools, 1)
		require.Equal(t, "list_issues", payload.Data.Tools[0].Name)
	})
}

func TestRESTDiscovery_ProfileSearchUsesPrelimitedProfileResults(t *testing.T) {
	controller := &globalToolsController{
		profileSearchResults: []map[string]interface{}{{
			"tool":  map[string]interface{}{"name": "list_issues", "server_name": "github", "description": "List issues"},
			"score": 2.0,
		}},
		profileSearchHandled: true,
	}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/search?q=issue&limit=1", http.NoBody).WithContext(pinnedDiscoveryContext())
	w := httptest.NewRecorder()
	srv.handleSearchTools(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var payload struct {
		Data struct {
			Results []struct {
				Tool struct {
					Name       string `json:"name"`
					ServerName string `json:"server_name"`
				} `json:"tool"`
			} `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Results, 1)
	require.Equal(t, "list_issues", payload.Data.Results[0].Tool.Name)
	require.Equal(t, "github", payload.Data.Results[0].Tool.ServerName)
}
