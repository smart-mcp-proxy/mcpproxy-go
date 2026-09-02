package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Harness for issues #1166 (REST enumeration ignores allowed_servers) and
// #1167 (reveal_secret_headers checked without caller identity).
//
// Everything here drives the PRODUCTION chi route table through
// (*Server).ServeHTTP and the real apiKeyAuthMiddleware. The token is a real
// mcp_agt_ token validated by the real handleAgentTokenAuth, so the tests
// exercise the same AuthContext a live agent gets — no hand-built router and no
// mock that answers for any input.

const (
	scopeAdminAPIKey  = "admin-api-key-for-scope-tests"
	alphaHeaderSecret = "SUPERSECRET_ALPHA_HEADER"
	alphaQuerySecret  = "SUPERSECRET_ALPHA_QUERY"
	betaArgvSecret    = "SUPERSECRET_BETA_ARGV"
	betaEnvSecret     = "SUPERSECRET_BETA_ENV"
	// betaHealthDetail is the free-text health detail the diagnostics doors
	// echo. It carries a URL query credential, which is the #872 shape
	// oauth.ScrubUpstreamText exists for.
	betaHealthDetail = `Post "https://beta.example.com/mcp?token=` + betaEnvSecret + `": no such host`
)

// scopeFixtureServers returns the two-server inventory every test in this
// group runs against: `alpha`, which the scoped token MAY see, and `beta`,
// which it may not. Both carry credentials, in all four classes the live
// reproduction found leaking (header, URL query, argv, env).
func scopeFixtureServers() []contracts.Server {
	return []contracts.Server{
		{
			ID:        "alpha",
			Name:      "alpha",
			Protocol:  "http",
			URL:       "https://alpha.example.com/mcp?token=" + alphaQuerySecret,
			Headers:   map[string]string{"Authorization": "Bearer " + alphaHeaderSecret},
			Enabled:   true,
			Connected: true,
			Status:    "Ready",
			ToolCount: 3,
		},
		{
			ID:        "beta",
			Name:      "beta",
			Protocol:  "stdio",
			Command:   "npx",
			Args:      []string{"beta-server", "--token=" + betaArgvSecret},
			Env:       map[string]string{"BETA_API_KEY": betaEnvSecret},
			Enabled:   true,
			Connected: true,
			Status:    "Ready",
			ToolCount: 7,
			LastError: betaHealthDetail,
			Health: &contracts.HealthStatus{
				Level:      "unhealthy",
				AdminState: "enabled",
				Summary:    "Connection error",
				Detail:     betaHealthDetail,
				Action:     "restart",
			},
		},
	}
}

func scopeFixtureConfig(reveal bool) *config.Config {
	return &config.Config{
		Listen:              "127.0.0.1:8080",
		APIKey:              scopeAdminAPIKey,
		RevealSecretHeaders: reveal,
		Servers: []*config.ServerConfig{
			{
				Name:     "alpha",
				Protocol: "http",
				URL:      "https://alpha.example.com/mcp?token=" + alphaQuerySecret,
				Headers:  map[string]string{"Authorization": "Bearer " + alphaHeaderSecret},
				Enabled:  true,
			},
			{
				Name:     "beta",
				Protocol: "stdio",
				Command:  "npx",
				Args:     []string{"beta-server", "--token=" + betaArgvSecret},
				Env:      map[string]string{"BETA_API_KEY": betaEnvSecret},
				Enabled:  true,
			},
		},
	}
}

// scopeMgmtService is the management-service half of the harness. It is a
// distinct type from mockManagementService so the fixture is under this test's
// control.
type scopeMgmtService struct {
	servers []contracts.Server
}

func (m *scopeMgmtService) ListServers(context.Context) ([]*contracts.Server, *contracts.ServerStats, error) {
	out := make([]*contracts.Server, 0, len(m.servers))
	stats := &contracts.ServerStats{}
	for i := range m.servers {
		srv := m.servers[i]
		out = append(out, &srv)
		stats.TotalServers++
		if srv.Connected {
			stats.ConnectedServers++
		}
		if srv.Quarantined {
			stats.QuarantinedServers++
		}
		stats.TotalTools += srv.ToolCount
	}
	return out, stats, nil
}

func (m *scopeMgmtService) GetServerTools(_ context.Context, name string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"name": name + "_tool", "server_name": name, "description": "tool of " + name},
	}, nil
}

// scopeController drives both handleGetServers branches. When withManagement is
// false GetManagementService returns an UNTYPED nil so the legacy fallback is
// genuinely selected — a typed nil pointer would still be a non-nil interface
// and would silently re-select the management branch, which is exactly how a
// one-branch fix ships looking complete.
type scopeController struct {
	MockServerController
	cfg            *config.Config
	servers        []contracts.Server
	withManagement bool

	mu   sync.Mutex
	subs []chan internalRuntime.Event
}

func (c *scopeController) GetManagementService() interface{} {
	if !c.withManagement {
		return nil
	}
	return &scopeMgmtService{servers: c.servers}
}

// GetCurrentConfig must return a real *config.Config or apiKeyAuthMiddleware
// forwards the request with NO AuthContext at all and every scoped assertion
// below would pass for the wrong reason.
func (c *scopeController) GetCurrentConfig() interface{} { return c.cfg }

func (c *scopeController) GetConfig() (*config.Config, error) { return c.cfg, nil }

func (c *scopeController) GetAllServers() ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(c.servers))
	for i := range c.servers {
		srv := c.servers[i]
		raw, err := json.Marshal(srv)
		if err != nil {
			return nil, err
		}
		var generic map[string]interface{}
		if err := json.Unmarshal(raw, &generic); err != nil {
			return nil, err
		}
		// GetAllServers hands health out as the typed pointer in production;
		// keep that shape so extractHealthFromMap and redactHealthDetail take
		// the same branch they take at runtime.
		generic["health"] = srv.Health
		out = append(out, generic)
	}
	return out, nil
}

func (c *scopeController) GetServerTools(name string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"name": name + "_tool", "server_name": name, "description": "tool of " + name},
	}, nil
}

func (c *scopeController) SearchTools(_ string, _ int) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(c.servers))
	for i := range c.servers {
		out = append(out, map[string]interface{}{
			"tool": map[string]interface{}{
				"name":        c.servers[i].Name + ":" + c.servers[i].Name + "_tool",
				"server_name": c.servers[i].Name,
			},
			"score": 0.9,
		})
	}
	return out, nil
}

func (c *scopeController) ListToolApprovals(_ string) ([]*storage.ToolApprovalRecord, error) {
	return nil, nil
}

// GetUpstreamStats mirrors the real producer's shape: a per-server map keyed by
// name PLUS the aggregate scalars that sit beside it. Literal ints, not a JSON
// round-trip: ConvertUpstreamStatsToServerStats type-asserts tool_count as
// .(int) and a float64 would silently zero TotalTools, making a
// "total_tools equals the allowed server's count" oracle pass trivially.
func (c *scopeController) GetUpstreamStats() map[string]interface{} {
	servers := map[string]interface{}{}
	connected, totalTools := 0, 0
	for i := range c.servers {
		servers[c.servers[i].Name] = map[string]interface{}{
			"name":        c.servers[i].Name,
			"connected":   c.servers[i].Connected,
			"connecting":  false,
			"quarantined": c.servers[i].Quarantined,
			"tool_count":  c.servers[i].ToolCount,
			"url":         c.servers[i].URL,
		}
		if c.servers[i].Connected {
			connected++
		}
		totalTools += c.servers[i].ToolCount
	}
	return map[string]interface{}{
		"servers":             servers,
		"total_servers":       len(c.servers),
		"connected_servers":   connected,
		"connecting_servers":  0,
		"quarantined_servers": 0,
		"total_tools":         totalTools,
	}
}

func (c *scopeController) GetStatus() interface{} {
	return map[string]interface{}{"phase": "Ready"}
}

// StatusChannel returns a live (never-closed) channel. The default mock closes
// it immediately, which makes the SSE handler's select spin on a closed channel
// and return before any runtime event is delivered.
func (c *scopeController) StatusChannel() <-chan interface{} {
	return make(chan interface{})
}

func (c *scopeController) SubscribeEvents() chan internalRuntime.Event {
	// Buffered well past the largest burst any test publishes (the whole
	// server-identity event class in one go). publishToAll gives up on a full
	// channel after 2s exactly as publishEvent drops immediately, so an
	// undersized buffer would silently turn a leak into a pass.
	ch := make(chan internalRuntime.Event, 128)
	c.mu.Lock()
	c.subs = append(c.subs, ch)
	c.mu.Unlock()
	return ch
}

func (c *scopeController) UnsubscribeEvents(ch chan internalRuntime.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, sub := range c.subs {
		if sub == ch {
			c.subs = append(c.subs[:i], c.subs[i+1:]...)
			return
		}
	}
}

func (c *scopeController) subscriberCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs)
}

// publishToAll fans ONE event value out to every subscriber, exactly as
// runtime.publishEvent does — same map pointer, same slice backing array. That
// sharing is the whole reason renderEventPayloadForCaller must never mutate.
func (c *scopeController) publishToAll(evt internalRuntime.Event) {
	c.mu.Lock()
	subs := append([]chan internalRuntime.Event(nil), c.subs...)
	c.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- evt:
		case <-time.After(2 * time.Second):
		}
	}
}

// scopedAgentServer wires a Server with a real agent token limited to
// allowedServers. agentTokenServer (tool_quarantine_test.go) hardcodes ["*"]
// and has no parameter for the scope, so this group needs its own.
func scopedAgentServer(t *testing.T, ctrl ServerController, allowedServers []string) (*Server, string) {
	t.Helper()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "scoped-ci",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: allowedServers,
		Permissions:    []string{auth.PermRead},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	store := &testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(store, tmpDir)
	return srv, rawToken
}

// scopeGet issues a GET through the production route table with the given
// X-API-Key and returns the recorder.
func scopeGet(t *testing.T, srv *Server, path, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// scopeDecodeData decodes the `data` object of the standard API envelope.
func scopeDecodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var envelope struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	require.True(t, envelope.Success, "body: %s", rec.Body.String())
	return envelope.Data
}

func scopeServerNames(t *testing.T, data map[string]interface{}) []string {
	t.Helper()
	raw, ok := data["servers"].([]interface{})
	require.True(t, ok, "no servers array in %#v", data)
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]interface{})
		require.True(t, ok)
		name, _ := m["name"].(string)
		names = append(names, name)
	}
	return names
}

func scopeServerEntry(t *testing.T, data map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	raw, ok := data["servers"].([]interface{})
	require.True(t, ok, "no servers array in %#v", data)
	for _, entry := range raw {
		m, ok := entry.(map[string]interface{})
		require.True(t, ok)
		if n, _ := m["name"].(string); n == name {
			return m
		}
	}
	t.Fatalf("server %q not present in response: %#v", name, data)
	return nil
}
