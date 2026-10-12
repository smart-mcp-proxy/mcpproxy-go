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

// Spec 116 PR2 review round 1 (FR-017): the tool listings carry the
// incarnation of the server their rows were read for, so the Web UI binds a
// per-tool decision to the data on screen rather than to whatever the live
// server list says when the operator clicks.

func TestGlobalTools_R1_RowsCarryServerIncarnation(t *testing.T) {
	ctrl := &globalToolsController{
		allServers: []map[string]interface{}{{"name": "github", "incarnation": int64(7)}, {"name": "memory"}},
		serverTools: map[string][]map[string]interface{}{
			"github": {{"name": "create_issue", "description": "Create issue"}},
			"memory": {{"name": "store", "description": "Store"}},
		},
	}
	data := doGlobalTools(t, ctrl)
	tools, ok := data["tools"].([]interface{})
	require.True(t, ok)
	require.Len(t, tools, 2)
	for _, raw := range tools {
		row := raw.(map[string]interface{})
		switch row["server_name"] {
		case "github":
			assert.Equal(t, float64(7), row["server_incarnation"])
		case "memory":
			_, has := row["server_incarnation"]
			assert.False(t, has, "a server without a committed incarnation offers none")
		}
	}
}

// incarnationToolsController adds the committed-incarnation reader to the
// management-backed tools controller.
type incarnationToolsController struct {
	*profileRESTDiscoveryController
	inc int64
}

func (c *incarnationToolsController) ServerIncarnation(string) (int64, bool) { return c.inc, c.inc > 0 }

func TestServerTools_R1_ResponseCarriesIncarnation(t *testing.T) {
	base := &profileRESTDiscoveryController{
		globalToolsController: &globalToolsController{
			allServers: []map[string]interface{}{{"name": "github", "id": "github"}},
			serverTools: map[string][]map[string]interface{}{
				"github": {{"name": "list_issues", "server_name": "github", "description": "List issues"}},
			},
		},
	}
	base.managementService = &profileRESTDiscoveryManagementService{controller: base.globalToolsController}
	ctrl := &incarnationToolsController{profileRESTDiscoveryController: base, inc: 9}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest("GET", "/api/v1/servers/github/tools", nil)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(9), data["incarnation"])
	tools := data["tools"].([]interface{})
	require.Len(t, tools, 1)
	assert.Equal(t, float64(9), tools[0].(map[string]interface{})["server_incarnation"])
}

// The approvals export the Server Detail toggles render from carries the
// incarnation too, read before the records.
func TestToolExport_R1_ResponseCarriesIncarnation(t *testing.T) {
	base := &profileRESTDiscoveryController{globalToolsController: &globalToolsController{
		allServers: []map[string]interface{}{{"name": "github", "id": "github"}},
	}}
	base.managementService = &profileRESTDiscoveryManagementService{controller: base.globalToolsController}
	ctrl := &incarnationToolsController{profileRESTDiscoveryController: base, inc: 4}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest("GET", "/api/v1/servers/github/tools/export", nil)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(4), resp["data"].(map[string]interface{})["incarnation"])
}

// The controller's own server projection may not carry the incarnation (the
// core's GetAllServers does not); the handler then asks the controller.
type incarnationGlobalToolsController struct {
	*globalToolsController
}

func (c *incarnationGlobalToolsController) ServerIncarnation(name string) (int64, bool) {
	return 11, name == "github"
}

func TestGlobalTools_R1_IncarnationFromControllerWhenRowHasNone(t *testing.T) {
	base := &globalToolsController{
		allServers:  []map[string]interface{}{{"name": "github"}},
		serverTools: map[string][]map[string]interface{}{"github": {{"name": "create_issue", "description": "Create issue"}}},
	}
	srv := NewServer(&incarnationGlobalToolsController{globalToolsController: base}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest("GET", "/api/v1/tools", nil)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	tools := resp["data"].(map[string]interface{})["tools"].([]interface{})
	require.Len(t, tools, 1)
	assert.Equal(t, float64(11), tools[0].(map[string]interface{})["server_incarnation"])
}
