package httpapi

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// Spec 116 PR2 review round 8 (#1064 + FR-017): the global tools listing
// takes its quarantine exclusion from the GetAllServers row, read before the
// tools. When the settled tool read moved to a same-name replacement, the
// exclusion must be the replacement's: a quarantined B contributes nothing,
// an unquarantined B is served under B's stamp.

// replacingGlobalController serves server "acme" as A (incarnation 3,
// unquarantined) until the first tool fetch, which replaces it with B
// (incarnation 7, quarantined per bQuarantined) before returning.
type replacingGlobalController struct {
	*globalToolsController
	bQuarantined bool

	mu       sync.Mutex
	replaced bool
}

func (c *replacingGlobalController) state() (int64, bool, []map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.replaced {
		return 3, false, []map[string]interface{}{{"name": "a_tool", "description": "A's tool"}}
	}
	return 7, c.bQuarantined, []map[string]interface{}{{"name": "b_tool", "description": "B's inspection definition"}}
}

func (c *replacingGlobalController) GetAllServers() ([]map[string]interface{}, error) {
	inc, q, _ := c.state()
	return []map[string]interface{}{{"name": "acme", "incarnation": inc, "quarantined": q}}, nil
}

func (c *replacingGlobalController) ServerIncarnation(string) (int64, bool) {
	inc, _, _ := c.state()
	return inc, true
}

func (c *replacingGlobalController) GetServerTools(string) ([]map[string]interface{}, error) {
	c.mu.Lock()
	c.replaced = true // the gate: B lands while the first fetch is in flight
	c.mu.Unlock()
	_, _, tools := c.state()
	return tools, nil
}

func newReplacingGlobalServer(t *testing.T, bQuarantined bool) *Server {
	ctrl := &replacingGlobalController{globalToolsController: &globalToolsController{}, bQuarantined: bQuarantined}
	return NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)
}

func TestGlobalTools_R8_QuarantinedReplacementContributesNothing(t *testing.T) {
	srv := newReplacingGlobalServer(t, true)
	code, resp := getJSONR7(t, srv, "/api/v1/tools")
	require.Equal(t, http.StatusOK, code, resp)
	data := resp["data"].(map[string]interface{})
	tools, _ := data["tools"].([]interface{})
	assert.Empty(t, tools, "a quarantined replacement's inspection definitions are never listed")
	stats := data["stats"].(map[string]interface{})
	assert.Equal(t, float64(0), stats["total"], "nor counted")
	assert.NotEqual(t, true, data["partial"], "a quarantine skip is not a failed fetch")
}

func TestGlobalTools_R8_UnquarantinedReplacementServedUnderItsStamp(t *testing.T) {
	srv := newReplacingGlobalServer(t, false)
	code, resp := getJSONR7(t, srv, "/api/v1/tools")
	require.Equal(t, http.StatusOK, code, resp)
	data := resp["data"].(map[string]interface{})
	tools, _ := data["tools"].([]interface{})
	require.Len(t, tools, 1, "control: an unquarantined replacement is listed")
	row := tools[0].(map[string]interface{})
	assert.Equal(t, "b_tool", row["name"])
	assert.Equal(t, float64(7), row["server_incarnation"])
}
