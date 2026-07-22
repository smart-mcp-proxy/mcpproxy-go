package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// adminConfigController wraps a ServerController so the auth middleware sees a
// real *config.Config (required to distinguish admin from agent tokens) with a
// known admin key.
type adminConfigController struct {
	ServerController
	apiKey string
}

func (c *adminConfigController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: c.apiKey}
}

// gateErrMsg is the exact body the requireServerOp gate writes. Both the agent
// (blocked) and admin (allowed) assertions key off it so the test verifies the
// gate specifically, independent of whatever the next layer returns.
const gateErrMsg = "operation requires admin access"

// mutatingServerRoutes enumerates every server-mutating REST route that must be
// denied to agent tokens (issues #877/#878). Adding a new mutating /servers
// route without gating it should make this table incomplete — keep it in sync.
var mutatingServerRoutes = []struct {
	name   string
	method string
	path   string
}{
	{"add", http.MethodPost, "/api/v1/servers"},
	{"import", http.MethodPost, "/api/v1/servers/import"},
	{"import-json", http.MethodPost, "/api/v1/servers/import/json"},
	{"import-path", http.MethodPost, "/api/v1/servers/import/path"},
	{"reconnect", http.MethodPost, "/api/v1/servers/reconnect"},
	{"restart-all", http.MethodPost, "/api/v1/servers/restart_all"},
	{"enable-all", http.MethodPost, "/api/v1/servers/enable_all"},
	{"disable-all", http.MethodPost, "/api/v1/servers/disable_all"},
	{"patch", http.MethodPatch, "/api/v1/servers/test-server"},
	{"remove", http.MethodDelete, "/api/v1/servers/test-server"},
	{"config-to-secret", http.MethodPost, "/api/v1/servers/test-server/config-to-secret"},
	{"enable", http.MethodPost, "/api/v1/servers/test-server/enable"},
	{"disable", http.MethodPost, "/api/v1/servers/test-server/disable"},
	{"restart", http.MethodPost, "/api/v1/servers/test-server/restart"},
	{"login", http.MethodPost, "/api/v1/servers/test-server/login"},
	{"logout", http.MethodPost, "/api/v1/servers/test-server/logout"},
	{"quarantine", http.MethodPost, "/api/v1/servers/test-server/quarantine"},
	{"unquarantine", http.MethodPost, "/api/v1/servers/test-server/unquarantine"},
	{"discover-tools", http.MethodPost, "/api/v1/servers/test-server/discover-tools"},
	{"refresh", http.MethodPost, "/api/v1/servers/test-server/refresh"},
	{"tools-approve", http.MethodPost, "/api/v1/servers/test-server/tools/approve"},
	{"tools-block", http.MethodPost, "/api/v1/servers/test-server/tools/block"},
	{"scan", http.MethodPost, "/api/v1/servers/test-server/scan"},
	{"scan-cancel", http.MethodPost, "/api/v1/servers/test-server/scan/cancel"},
	{"security-approve", http.MethodPost, "/api/v1/servers/test-server/security/approve"},
	{"security-reject", http.MethodPost, "/api/v1/servers/test-server/security/reject"},
	// Sibling routes that mutate server/registry state OUTSIDE /servers/{id} —
	// an agent must not bypass the /servers gate through them (issue #878).
	{"config-apply", http.MethodPost, "/api/v1/config/apply"},
	{"config-patch", http.MethodPatch, "/api/v1/config"},
	{"config-docker-isolation", http.MethodPatch, "/api/v1/config/docker-isolation"},
	{"registry-add-source", http.MethodPost, "/api/v1/registries"},
	{"registry-edit-source", http.MethodPut, "/api/v1/registries/reg1"},
	{"registry-remove-source", http.MethodDelete, "/api/v1/registries/reg1"},
	{"registry-add-server", http.MethodPost, "/api/v1/registries/reg1/servers/srv1/add"},
	{"secret-set", http.MethodPost, "/api/v1/secrets"},
	{"secret-delete", http.MethodDelete, "/api/v1/secrets/tok"},
	{"secret-migrate", http.MethodPost, "/api/v1/secrets/migrate"},
	{"diagnostics-fix", http.MethodPost, "/api/v1/diagnostics/fix"},
	{"scanner-enable", http.MethodPost, "/api/v1/security/scanners/s1/enable"},
	{"scanner-disable", http.MethodPost, "/api/v1/security/scanners/s1/disable"},
	{"scanner-config", http.MethodPut, "/api/v1/security/scanners/s1/config"},
	{"scanner-install", http.MethodPost, "/api/v1/security/scanners/install"},
	{"scanner-remove", http.MethodDelete, "/api/v1/security/scanners/s1"},
	{"scan-all", http.MethodPost, "/api/v1/security/scan-all"},
	{"cancel-all", http.MethodPost, "/api/v1/security/cancel-all"},
}

// TestMutatingServerRoutes_AgentTokenForbidden asserts every mutating server
// route rejects an agent token with 403 (issues #877/#878). The agent is scoped
// to all servers with read+write permission, so the denial is the operation
// policy — not a scope/permission miss.
func TestMutatingServerRoutes_AgentTokenForbidden(t *testing.T) {
	for _, rt := range mutatingServerRoutes {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
			srv, agentToken := agentTokenServer(t, ctrl)

			req := httptest.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("X-API-Key", agentToken)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code, "agent token must be forbidden")
			assert.Contains(t, w.Body.String(), "admin access")
		})
	}
}

// TestMutatingServerRoutes_AdminAllowed asserts a full API key is never blocked
// by the agent-token gate — it reaches the next layer (whatever status that
// returns), proving back-compat for admin callers (issues #877/#878).
func TestMutatingServerRoutes_AdminAllowed(t *testing.T) {
	const adminKey = "admin-secret"
	for _, rt := range mutatingServerRoutes {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: adminKey}
			logger := zap.NewNop().Sugar()
			srv := NewServer(ctrl, logger, nil)

			req := httptest.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("X-API-Key", adminKey)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			require.NotEqual(t, http.StatusUnauthorized, w.Code, "admin key must authenticate")
			// The gate must not fire for admin: its exact message must be absent.
			assert.NotContains(t, w.Body.String(), gateErrMsg,
				"admin request must pass the agent-token gate (route %s)", rt.path)
		})
	}
}
