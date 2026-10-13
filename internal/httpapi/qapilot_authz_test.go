package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Quarantine/approval testing pilot — REST authorization (QP-05).
//
// The policy model says which caller may perform which server-mutating
// operation. For every denied (agent, operation) pair the pilot asserts BOTH
// halves of a denial: the 403 response AND the absence of the side effect —
// the controller behind the handler is never invoked. For the admin caller
// the same requests must reach the controller.

// qapilotCountingController counts every mutating controller call so a
// denial can be proven side-effect free.
type qapilotCountingController struct {
	*MockServerController
	apiKey string
	calls  atomic.Int64
}

func (c *qapilotCountingController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: c.apiKey}
}

// GetManagementService returns nil so enable/disable take the controller
// path (EnableServer) the counter can observe.
func (c *qapilotCountingController) GetManagementService() management.Service { return nil }
func (c *qapilotCountingController) QuarantineServer(_ string, _ bool) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) EnableServer(_ string, _ bool) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) UpdateServer(_ context.Context, _ string, _ *config.ServerConfig) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) ApproveTools(_ string, _ []string, _ string) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) ApproveAllTools(_ string, _ string) (int, error) {
	c.calls.Add(1)
	return 0, nil
}
func (c *qapilotCountingController) BlockTools(_ string, _ []string, _ string) (int, error) {
	c.calls.Add(1)
	return 0, nil
}
func (c *qapilotCountingController) ApplyConfig(_ *config.Config, _ string) (*internalRuntime.ConfigApplyResult, error) {
	c.calls.Add(1)
	return &internalRuntime.ConfigApplyResult{}, nil
}
func (c *qapilotCountingController) AddServer(_ context.Context, _ *config.ServerConfig) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) RemoveServer(_ context.Context, _ string) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) RestartServer(_ string) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) TriggerOAuthLogin(_ string) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) DiscoverServerTools(_ context.Context, _ string) error {
	c.calls.Add(1)
	return nil
}
func (c *qapilotCountingController) AddServerFromRegistryRef(_ context.Context, _, _, _ string, _ map[string]string, _ *bool) (*config.ServerConfig, *contracts.RegistryAddError, error) {
	c.calls.Add(1)
	return &config.ServerConfig{Name: "x"}, nil, nil
}

// qapilotRoutes maps the policy's operation names onto the REST routes that
// perform them. Only operations whose controller call the counting
// controller can observe are listed with a body; the remaining denied
// operations are still asserted for the 403.
var qapilotRoutes = []struct {
	op     string
	method string
	path   string
	body   string
}{
	{"unquarantine", http.MethodPost, "/api/v1/servers/test-server/unquarantine", ""},
	{"quarantine", http.MethodPost, "/api/v1/servers/test-server/quarantine", ""},
	{"enable", http.MethodPost, "/api/v1/servers/test-server/enable", ""},
	{"disable", http.MethodPost, "/api/v1/servers/test-server/disable", ""},
	{"approve_tools", http.MethodPost, "/api/v1/servers/test-server/tools/approve", `{"approve_all":true}`},
	{"block_tools", http.MethodPost, "/api/v1/servers/test-server/tools/block", `{"tools":["t1"]}`},
	{"patch", http.MethodPatch, "/api/v1/servers/test-server", `{"quarantined":false}`},
	{"security_approve", http.MethodPost, "/api/v1/servers/test-server/security/approve", `{"force":true}`},
	{"security_reject", http.MethodPost, "/api/v1/servers/test-server/security/reject", ""},
	{"scan", http.MethodPost, "/api/v1/servers/test-server/scan", ""},
	{"config_write", http.MethodPatch, "/api/v1/config", `{"quarantine_enabled":false}`},
	{"add", http.MethodPost, "/api/v1/servers", `{"name":"x","url":"http://127.0.0.1:1/mcp","protocol":"http","quarantined":false}`},
	{"remove", http.MethodDelete, "/api/v1/servers/test-server", ""},
	{"restart", http.MethodPost, "/api/v1/servers/test-server/restart", ""},
	{"refresh", http.MethodPost, "/api/v1/servers/test-server/refresh", ""},
	{"discover_tools", http.MethodPost, "/api/v1/servers/test-server/discover-tools", ""},
	{"login", http.MethodPost, "/api/v1/servers/test-server/login", ""},
	{"logout", http.MethodPost, "/api/v1/servers/test-server/logout", ""},
	{"config_to_secret", http.MethodPost, "/api/v1/servers/test-server/config-to-secret", ""},
	{"add_from_registry", http.MethodPost, "/api/v1/registries/reg1/servers/srv1/add", ""},
	{"secret_write", http.MethodPost, "/api/v1/secrets", `{"name":"a","value":"b"}`},
	{"diagnostics_fix", http.MethodPost, "/api/v1/diagnostics/fix", ""},
}

// requireExactGateBody asserts the response is ONE JSON document holding
// exactly the gate's error and nothing else. A handler that ran after the
// gate would append its own document (or fields) to the body, so this is a
// structural witness for every route, including those whose controller
// method the counting controller does not override.
func requireExactGateBody(t *testing.T, body []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	var doc map[string]json.RawMessage
	require.NoError(t, dec.Decode(&doc), "gate body must be JSON: %s", body)
	require.False(t, dec.More(), "the body must hold a single JSON document (a handler wrote after the gate): %s", body)
	var errText string
	require.NoError(t, json.Unmarshal(doc["error"], &errText))
	require.Equal(t, gateErrMsg, errText)
	for k := range doc {
		switch k {
		case "success", "error", "request_id":
		default:
			t.Fatalf("unexpected field %q in the gate body (a handler wrote after the gate): %s", k, body)
		}
	}
}

func TestQAPilot_AgentAuthorizationTable(t *testing.T) {
	qapilot.Track(t, "authz/table", "QP-05")
	// Every operation the policy denies to agents must be exercised by a
	// route here; an unmapped operation is a gap in the pilot, not a pass.
	mapped := map[string]bool{}
	for _, r := range qapilotRoutes {
		mapped[r.op] = true
	}
	// "update" has no REST route of its own (REST exposes PATCH only); it is
	// an MCP upstream_servers operation, covered by the MCP checks in
	// internal/server (QP-06).
	mcpOnly := map[string]bool{"update": true}
	for _, op := range qapilot.AgentDeniedOps {
		require.Truef(t, mapped[op] || mcpOnly[op], "policy operation %q has no REST route in the pilot table", op)
	}

	for _, r := range qapilotRoutes {
		r := r
		t.Run("agent/"+r.op, func(t *testing.T) {
			require.False(t, qapilot.Authorize(qapilot.CallerAgent, r.op), "policy: agents are denied %q", r.op)
			ctrl := &qapilotCountingController{MockServerController: &MockServerController{}, apiKey: "admin-secret"}
			srv, agentToken := agentTokenServer(t, ctrl)
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("X-API-Key", agentToken)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, "agent must get 403 for %s %s: %s", r.method, r.path, w.Body.String())
			// The exact gate body: requireServerOp writes it and returns
			// WITHOUT calling the wrapped handler, so this body is itself
			// evidence that no handler (and so no controller) ran. The call
			// counter below is the direct observation for the operations
			// whose controller method the mock exposes.
			requireExactGateBody(t, w.Body.Bytes())
			require.Zero(t, ctrl.calls.Load(), "a denied agent request must not reach the controller (%s)", r.op)
		})
		t.Run("admin/"+r.op, func(t *testing.T) {
			require.True(t, qapilot.Authorize(qapilot.CallerAdmin, r.op))
			ctrl := &qapilotCountingController{MockServerController: &MockServerController{}, apiKey: "admin-secret"}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("X-API-Key", "admin-secret")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusUnauthorized, w.Code)
			require.NotEqual(t, http.StatusForbidden, w.Code, "admin must pass the gate for %s: %s", r.op, w.Body.String())
			require.NotContains(t, w.Body.String(), gateErrMsg)
		})
	}

	// Side-effect check for the operations whose controller call is
	// observable: with an admin key the same requests DO reach the
	// controller, so the zero count above is a real denial, not a route
	// that never calls anything.
	// login is routed through the management service (nil here) before any
	// controller call, so it has no positive control; its denial is still
	// asserted above.
	for _, op := range []string{"unquarantine", "quarantine", "enable", "disable", "approve_tools", "block_tools", "patch", "add", "remove", "config_write", "restart", "refresh", "discover_tools", "add_from_registry"} {
		var route *struct {
			op     string
			method string
			path   string
			body   string
		}
		for i := range qapilotRoutes {
			if qapilotRoutes[i].op == op {
				route = &qapilotRoutes[i]
			}
		}
		require.NotNil(t, route)
		ctrl := &qapilotCountingController{MockServerController: &MockServerController{}, apiKey: "admin-secret"}
		srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("X-API-Key", "admin-secret")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Positive(t, ctrl.calls.Load(), "admin %s must reach the controller (status %d: %s)", op, w.Code, w.Body.String())
	}
}
