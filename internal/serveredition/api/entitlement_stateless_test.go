//go:build server

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Spec 058 US3, FR-013 (server edition): multiuser tool filtering must survive
// the stateless 2026-07-28 era. Research D4 found this the one untested half of
// FR-013 — the server-edition suite was green under v1.0.0, but nothing drove
// two OAuth users with differing entitlements over modern requests, where no
// session exists to carry identity between calls.
//
// What this test exercises, precisely: the production entitlement predicate
// (UserHandlers.ResolveAgentTokenOwner, the same call setup.go's
// buildSessionPrincipalResolver makes) derived per request from a bearer JWT,
// and the stateless era of the mcp-go transport. The JWT → AuthContext step
// and the tool filter are test-local stand-ins, because this package cannot
// import internal/server (import cycle through serveredition_wire.go), so the
// production MCP auth middleware and direct-surface filters are NOT on this
// path. Their modern-era behaviour is pinned in internal/server:
// TestStatelessList_AgentTokensScopedPerRequest drives two agent tokens with
// differing allowed_servers — the credential a server-edition user actually
// reaches /mcp with — through mcpAuthMiddleware over the era harness.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

const (
	statelessUserA = "01HTEST0000000000STATELESSA"
	statelessUserB = "01HTEST0000000000STATELESSB"
)

// statelessProbe records what the MCP server observed about each request's
// session and era.
type statelessProbe struct {
	mu         sync.Mutex
	sessionIDs []string
	legacySeen int
}

func (p *statelessProbe) observe(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		p.sessionIDs = append(p.sessionIDs, sess.SessionID())
	}
	if !mcpserver.IsModernRequest(ctx) {
		p.legacySeen++
	}
}

// ownerOf reads the server a direct-surface tool name belongs to.
func ownerOf(toolName string) string {
	owner, _, _ := strings.Cut(toolName, "__")
	return owner
}

func TestEntitlementStateless_TwoUsersIsolatedWithoutSession(t *testing.T) {
	store := newFixtureUserStore(t)
	now := time.Now().UTC()
	for _, u := range []struct{ id, email, name string }{
		{statelessUserA, "a@example.com", "User A"},
		{statelessUserB, "b@example.com", "User B"},
	} {
		require.NoError(t, store.CreateUser(&users.User{
			ID: u.id, Email: u.email, DisplayName: u.name, Provider: "google",
			ProviderSubjectID: "sub-" + u.id, CreatedAt: now, LastLoginAt: now,
		}))
	}
	// Differing entitlements: both reach the shared server, only B has a
	// personal one.
	require.NoError(t, store.CreateUserServer(statelessUserB, &config.ServerConfig{
		Name: "b-private", URL: "https://b.internal/mcp", Protocol: "http", Enabled: true,
	}))
	shared := defaultSharedServers() // shared-github
	handlers := NewUserHandlers(store, func() []*config.ServerConfig { return shared }, nil, nil, zap.NewNop().Sugar())

	hmacKey := tokenTestHMACKey
	tokenA, err := teamsauth.GenerateBearerToken(hmacKey, statelessUserA, "a@example.com", "User A", "user", "google", time.Hour)
	require.NoError(t, err)
	tokenB, err := teamsauth.GenerateBearerToken(hmacKey, statelessUserB, "b@example.com", "User B", "user", "google", time.Hour)
	require.NoError(t, err)

	// resolvePrincipal is buildSessionPrincipalResolver's bearer branch, per
	// request: validate the JWT, load the user, materialise the entitlement.
	resolvePrincipal := func(ctx context.Context, r *http.Request) context.Context {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, err := teamsauth.ValidateBearerToken(raw, hmacKey)
		if err != nil {
			return ctx
		}
		owner, err := handlers.ResolveAgentTokenOwner(claims.Subject, []string{"*"}, nil)
		if err != nil || !owner.Active {
			return ctx
		}
		ac := auth.UserContext(owner.UserID, owner.Email, "", owner.Provider)
		ac.AllowedServers = owner.Entitled
		ac.CredentialKind = auth.CredentialKindBearerJWT
		return auth.WithAuthContext(ctx, ac)
	}

	probe := &statelessProbe{}
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, _ any, _ mcp.MCPMethod, _ any) { probe.observe(ctx) })

	// The entitlement filter: a tool is visible and callable only when the
	// request's identity is entitled to its owning server. An unidentified
	// request sees nothing (fail closed).
	entitled := func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
		ac := auth.AuthContextFromContext(ctx)
		out := make([]mcp.Tool, 0, len(tools))
		for _, tool := range tools {
			if ac != nil && ac.CanAccessServer(ownerOf(tool.Name)) {
				out = append(out, tool)
			}
		}
		return out
	}
	srv := mcpserver.NewMCPServer("se-probe", "1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithToolFilter(entitled),
		mcpserver.WithHooks(hooks),
	)
	for _, name := range []string{"shared-github__list_repos", "b-private__read_secret"} {
		name := name
		srv.AddTool(mcp.NewTool(name, mcp.WithDescription(name)),
			func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok:" + name), nil
			})
	}
	// Unpinned: the modern era is served (the FR-028 pin applies to mcpproxy's
	// own listening endpoint, which is what T041 lifts).
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithHTTPContextFunc(resolvePrincipal)))
	t.Cleanup(httpServer.Close)

	connect := func(token string) *client.Client {
		tr, err := transport.NewStreamableHTTP(httpServer.URL,
			transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
		require.NoError(t, err)
		c := client.NewClient(tr, client.WithProtocolVersion(mcp.LATEST_PROTOCOL_VERSION))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		require.NoError(t, c.Start(ctx))
		t.Cleanup(func() { _ = c.Close() })
		_, err = c.Initialize(ctx, mcp.InitializeRequest{})
		require.NoError(t, err)
		require.Equal(t, mcp.LATEST_PROTOCOL_VERSION, c.ProtocolVersion(),
			"this test asserts modern-era behaviour; a legacy negotiation would make it vacuous")
		require.Empty(t, tr.GetSessionId(), "the modern era binds no session id")
		return c
	}
	listNames := func(c *client.Client) []string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		require.NoError(t, err)
		names := make([]string, 0, len(res.Tools))
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}
		return names
	}
	call := func(c *client.Client, name string) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = map[string]any{}
		return c.CallTool(ctx, req)
	}

	a := connect(tokenA)
	b := connect(tokenB)

	// Interleaved, concurrent-style use: identity is re-derived per request, so
	// A's view is unchanged by B's requests in between.
	first := listNames(a)
	assert.Equal(t, []string{"shared-github__list_repos"}, first, "A is entitled to the shared server only")
	assert.ElementsMatch(t, []string{"shared-github__list_repos", "b-private__read_secret"}, listNames(b), "B is entitled to the shared server and its own")
	assert.Equal(t, first, listNames(a), "A's view must not drift after another identity's requests")

	// Calls are gated by the same request-carried entitlement.
	_, err = call(a, "b-private__read_secret")
	require.Error(t, err, "A must not be able to call B's private tool")
	res, err := call(b, "b-private__read_secret")
	require.NoError(t, err)
	require.False(t, res.IsError)
	res, err = call(a, "shared-github__list_repos")
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Nothing above relied on a session: every request ran under an empty id
	// in the modern era.
	probe.mu.Lock()
	defer probe.mu.Unlock()
	require.NotEmpty(t, probe.sessionIDs, "the probe must have observed the requests")
	for _, sid := range probe.sessionIDs {
		assert.Equal(t, "", sid, "a modern request must carry no session id")
	}
	assert.Zero(t, probe.legacySeen, "every request must have been served on the modern path")
}
