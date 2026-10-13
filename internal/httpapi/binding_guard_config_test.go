package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func guardRefusal() *runtime.BindingGuardError {
	return &runtime.BindingGuardError{
		Bindings: []runtime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}},
		Fixes: []runtime.GuardFix{
			{Kind: "require_mcp_auth"},
			{Kind: "set_anonymous_profile", Target: "ro"},
		},
	}
}

// TestConfigRoutes_MapBindingGuardRefusalTo409 pins D5: every REST config
// writer funnels through controller.ApplyConfig, and its *BindingGuardError
// becomes the byte-stable 409 body.
func TestConfigRoutes_MapBindingGuardRefusalTo409(t *testing.T) {
	routes := []struct {
		name, method, path, body string
	}{
		{"PATCH /config", http.MethodPatch, "/api/v1/config", `{"require_mcp_auth":false}`},
		{"POST /config/apply", http.MethodPost, "/api/v1/config/apply", `{"listen":"127.0.0.1:8080","require_mcp_auth":false}`},
		{"PATCH /config/docker-isolation", http.MethodPatch, "/api/v1/config/docker-isolation", `{"enabled":true}`},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			ctrl := &applyErrController{err: guardRefusal()}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewReader([]byte(rt.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "k")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, false, body["success"])
			require.Equal(t, "binding_bypassable_without_auth", body["code"])
			require.Equal(t, "a client bound to profile ro could escape it by omitting its credential while require_mcp_auth is off", body["error"])
			require.Contains(t, body, "request_id")
			require.Equal(t, []interface{}{
				map[string]interface{}{"client_id": "cursor", "token_name": "client-cursor", "profile": "ro", "mode": "locked"},
			}, body["bindings"])
			require.Equal(t, []interface{}{
				map[string]interface{}{"kind": "require_mcp_auth"},
				map[string]interface{}{"kind": "set_anonymous_profile", "target": "ro"},
			}, body["fixes"])
		})
	}
}
