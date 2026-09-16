package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newProfileGateTestServer builds a Server whose logger is observed, with two
// configured servers and two single-server profiles, so profileMiddleware
// can be driven directly with a hand-built AuthContext (auth already ran).
func newProfileGateTestServer(t *testing.T) (*Server, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Servers = []*config.ServerConfig{{Name: "research-srv"}, {Name: "deploy-srv"}}
	cfg.Profiles = []config.ProfileConfig{
		{Name: "research", Servers: []string{"research-srv"}},
		{Name: "deploy", Servers: []string{"deploy-srv"}},
	}

	srv, err := NewServer(cfg, zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv, logs
}

// TestProfileMiddleware_ScopedRefusalIsLoggedForOperator (Spec 105 PR D
// critique round 1, finding S1): the uniform FR-004 refusal is deliberately
// silent towards the AGENT, but it must not be silent towards the OPERATOR.
// The gate answers before the request reaches the logging handler mounted
// inside it, so without its own log line a scoped token walking the slug
// space of /mcp/p/ leaves no trace at all. One structured line per refusal,
// naming the agent, the slug it asked for and where it came from — and none
// on admission.
func TestProfileMiddleware_ScopedRefusalIsLoggedForOperator(t *testing.T) {
	srv, logs := newProfileGateTestServer(t)

	reached := false
	handler := srv.profileMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	agent := &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "a-only", AllowedServers: []string{"research-srv"}}

	req := httptest.NewRequest(http.MethodPost, "/mcp/p/deploy", http.NoBody)
	req.RemoteAddr = "203.0.113.7:4242"
	req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.False(t, reached, "a non-selectable slug must not reach the MCP handler")

	refusals := logs.FilterMessage("profile URL refused for scoped caller").All()
	require.Len(t, refusals, 1, "exactly one operator-facing line per refusal")
	fields := refusals[0].ContextMap()
	require.Equal(t, "a-only", fields["agent_name"])
	require.Equal(t, "deploy", fields["profile"])
	require.Equal(t, "203.0.113.7:4242", fields["remote_addr"])

	// Admission through a selectable profile is not a refusal and logs none.
	logs.TakeAll()
	req = httptest.NewRequest(http.MethodPost, "/mcp/p/research", http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.True(t, reached, "the selectable profile must be admitted")
	require.Empty(t, logs.FilterMessage("profile URL refused for scoped caller").All())
}
