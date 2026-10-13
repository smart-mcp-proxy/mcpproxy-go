package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 116 PR2 (FR-017, contracts/surfaces.md): the approval endpoints pass
// "expected_incarnation" to the controller and answer 409 server_replaced on
// a mismatch, 404 for a server that is not configured.

// incarnationController is the tool quarantine mock plus the bound writers:
// the server "github" currently has incarnation 7.
type incarnationController struct {
	mockToolQuarantineController
	lastExpected int64
	calls        []string
}

const currentIncarnation = 7

func (c *incarnationController) check(op, server string, expected int64) error {
	c.calls = append(c.calls, op)
	c.lastExpected = expected
	if server != "github" {
		return &internalRuntime.ServerNotFoundError{Name: server}
	}
	if expected != 0 && expected != currentIncarnation {
		return &commitview.ServerReplacedError{Name: server}
	}
	return nil
}

func (c *incarnationController) ApproveToolsAt(server string, _ []string, _ string, inc int64) error {
	return c.check("approve", server, inc)
}
func (c *incarnationController) ApproveAllToolsAt(server, _ string, inc int64) (int, error) {
	return 0, c.check("approve_all", server, inc)
}
func (c *incarnationController) BlockToolsAt(server string, _ []string, _ string, inc int64) (int, error) {
	return 0, c.check("block", server, inc)
}
func (c *incarnationController) BlockAllToolsAt(server, _ string, inc int64) (int, error) {
	return 0, c.check("block_all", server, inc)
}
func (c *incarnationController) SetToolEnabledAt(server, _ string, _ bool, _ string, inc int64) error {
	return c.check("enabled", server, inc)
}
func (c *incarnationController) SetAllToolsEnabledAt(server string, _ bool, _ string, inc int64) (int, error) {
	return 0, c.check("enable_all", server, inc)
}
func (c *incarnationController) QuarantineServerAt(server string, _ bool, inc int64) error {
	return c.check("unquarantine", server, inc)
}

func TestExpectedIncarnation_FR017_EndpointsMapServerReplaced(t *testing.T) {
	cases := []struct {
		op, path, body string
	}{
		{"approve", "/api/v1/servers/github/tools/approve", `{"tools":["a"],"expected_incarnation":%d}`},
		{"approve_all", "/api/v1/servers/github/tools/approve", `{"approve_all":true,"expected_incarnation":%d}`},
		{"block", "/api/v1/servers/github/tools/block", `{"tools":["a"],"expected_incarnation":%d}`},
		{"block_all", "/api/v1/servers/github/tools/block", `{"block_all":true,"expected_incarnation":%d}`},
		{"enabled", "/api/v1/servers/github/tools/a/enabled", `{"enabled":false,"expected_incarnation":%d}`},
		{"enable_all", "/api/v1/servers/github/tools/disable_all", `{"expected_incarnation":%d}`},
		{"unquarantine", "/api/v1/servers/github/unquarantine", `{"expected_incarnation":%d}`},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			ctrl := &incarnationController{mockToolQuarantineController: mockToolQuarantineController{apiKey: "test-key"}}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			do := func(inc int64) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(fmt.Sprintf(tc.body, inc)))
				req.Header.Set("X-API-Key", "test-key")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				return w
			}

			w := do(3)
			require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
			var resp map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, false, resp["success"])
			assert.Equal(t, "server_replaced: server 'github' was removed or replaced since it was reviewed", resp["error"])
			assert.Equal(t, int64(3), ctrl.lastExpected)
			assert.Contains(t, ctrl.calls, tc.op)

			w = do(currentIncarnation)
			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		})
	}
}

// Without the field the endpoint stays unbound (the runtime binds to the
// incarnation current when the request starts) and keeps its old answer.
func TestExpectedIncarnation_FR017_AbsentFieldIsUnbound(t *testing.T) {
	ctrl := &incarnationController{mockToolQuarantineController: mockToolQuarantineController{apiKey: "test-key"}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/github/unquarantine", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	req = httptest.NewRequest(http.MethodPost, "/api/v1/servers/github/tools/approve", bytes.NewBufferString(`{"tools":["a"]}`))
	req.Header.Set("X-API-Key", "test-key")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, int64(0), ctrl.lastExpected)
}
