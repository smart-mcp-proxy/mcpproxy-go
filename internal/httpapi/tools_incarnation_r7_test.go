package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// Spec 116 PR2 review round 7 (FR-017): a listing reads the incarnation
// before its rows. If a same-name server lands between those reads (the
// incarnation read saw the name absent, or saw the removed server), the rows
// belong to a server the stamp does not name. The handler re-reads the
// incarnation after the rows and only answers when it did not move; a moving
// identity is re-read, and one that never settles is refused (409).

// sequencedIncarnationController reports the incarnations in seq, one per
// call, then repeats the last one.
type sequencedIncarnationController struct {
	*profileRESTDiscoveryController
	mu  sync.Mutex
	seq []int64
}

func (c *sequencedIncarnationController) ServerIncarnation(string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.seq[0]
	if len(c.seq) > 1 {
		c.seq = c.seq[1:]
	}
	return v, v > 0
}

func newSequencedIncarnationServer(t *testing.T, seq ...int64) *Server {
	base := &profileRESTDiscoveryController{
		globalToolsController: &globalToolsController{
			allServers: []map[string]interface{}{{"name": "github", "id": "github"}},
			serverTools: map[string][]map[string]interface{}{
				"github": {{"name": "list_issues", "server_name": "github", "description": "List issues"}},
			},
		},
	}
	base.managementService = &profileRESTDiscoveryManagementService{controller: base.globalToolsController}
	ctrl := &sequencedIncarnationController{profileRESTDiscoveryController: base, seq: seq}
	return NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)
}

func getJSONR7(t *testing.T, srv *Server, path string) (int, map[string]interface{}) {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	req.Header.Set("X-Request-Source", "socket")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestServerTools_R7_ReplacementDuringReadIsRereadNotServedUnbound(t *testing.T) {
	// absent before the rows, B (5) after: the response must carry 5.
	srv := newSequencedIncarnationServer(t, 0, 5)
	code, resp := getJSONR7(t, srv, "/api/v1/servers/github/tools")
	require.Equal(t, http.StatusOK, code, resp)
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(5), data["incarnation"], "rows read while B landed are bound to B, never unbound")
	for _, raw := range data["tools"].([]interface{}) {
		assert.Equal(t, float64(5), raw.(map[string]interface{})["server_incarnation"])
	}
}

func TestToolExport_R7_ReplacementDuringReadIsRereadNotServedUnbound(t *testing.T) {
	srv := newSequencedIncarnationServer(t, 0, 5)
	code, resp := getJSONR7(t, srv, "/api/v1/servers/github/tools/export")
	require.Equal(t, http.StatusOK, code, resp)
	assert.Equal(t, float64(5), resp["data"].(map[string]interface{})["incarnation"])
}

func TestServerTools_R7_IdentityThatNeverSettlesIsRefused(t *testing.T) {
	srv := newSequencedIncarnationServer(t, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	code, _ := getJSONR7(t, srv, "/api/v1/servers/github/tools")
	assert.Equal(t, http.StatusConflict, code)
	srv = newSequencedIncarnationServer(t, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	code, _ = getJSONR7(t, srv, "/api/v1/servers/github/tools/export")
	assert.Equal(t, http.StatusConflict, code)
}

// The global listing stamps each server's rows with the incarnation read
// before them; a server replaced mid-listing is reported partial, not served
// under the wrong stamp.
type sequencedGlobalController struct {
	*globalToolsController
	mu  sync.Mutex
	seq []int64
}

func (c *sequencedGlobalController) ServerIncarnation(string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.seq[0]
	if len(c.seq) > 1 {
		c.seq = c.seq[1:]
	}
	return v, v > 0
}

func TestGlobalTools_R7_ReplacementDuringReadNotServedUnderOldStamp(t *testing.T) {
	base := &globalToolsController{
		allServers:  []map[string]interface{}{{"name": "github"}},
		serverTools: map[string][]map[string]interface{}{"github": {{"name": "create_issue", "description": "Create issue"}}},
	}
	srv := NewServer(&sequencedGlobalController{globalToolsController: base, seq: []int64{0, 5}}, zaptest.NewLogger(t).Sugar(), nil)
	code, resp := getJSONR7(t, srv, "/api/v1/tools")
	require.Equal(t, http.StatusOK, code, resp)
	data := resp["data"].(map[string]interface{})
	tools, _ := data["tools"].([]interface{})
	for _, raw := range tools {
		inc, has := raw.(map[string]interface{})["server_incarnation"]
		assert.True(t, has && inc == float64(5), "rows are never served unbound or under a stale stamp: %v", raw)
	}
}
