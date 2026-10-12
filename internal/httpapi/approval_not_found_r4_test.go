package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// unboundNotFoundController answers an unbound unquarantine of an
// unconfigured name the way the runtime does.
type unboundNotFoundController struct {
	incarnationController
}

func (c *unboundNotFoundController) QuarantineServer(server string, _ bool) error {
	return c.check("unquarantine", server, 0)
}

// Spec 116 PR2 review round 4 (finding 4): docs/api/rest-api.md promises 404
// for an approval write on a server that is not configured. Unquarantine
// without expected_incarnation answered 500, security/approve 409.
func TestApprovalWrites_R4_MissingServerIs404(t *testing.T) {
	t.Run("unquarantine", func(t *testing.T) {
		ctrl := &unboundNotFoundController{incarnationController{mockToolQuarantineController: mockToolQuarantineController{apiKey: "test-key"}}}
		srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/ghost/unquarantine", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	})
	t.Run("security_approve", func(t *testing.T) {
		secCtrl := &mockSecurityController{approveErr: &internalRuntime.ServerNotFoundError{Name: "ghost"}}
		srv := newTestServerWithSecurity(t, secCtrl)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/ghost/security/approve", bytes.NewBufferString(`{}`))
		req.Header.Set("X-API-Key", mockControllerAPIKey)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	})
}
