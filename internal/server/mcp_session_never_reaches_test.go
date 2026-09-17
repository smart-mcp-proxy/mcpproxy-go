//go:build server

package server

// Spec 107 T068 (US1, FR-043(a)): regression pin, not a red test.
//
// FR-043(a) became true in PR-B (T052, mcpAuthMiddleware forced under
// config.EffectiveRequireMCPAuth) and is exercised at length by
// TestMCPAuthMiddleware_ForcedUnderServerEdition in mcp_auth_forced_test.go.
// This file is the T068 pin PR-C carries forward, covering two things that
// file does not:
//
//  1. every /mcp* route — not just "/mcp" — refuses a session cookie, a user
//     JWT, and (with server_edition.enabled forcing the effective value)
//     no credential at all, even when the raw require_mcp_auth field is set
//     to false;
//  2. the cache-authorization gate for a session principal
//     (cache/authorization.go:103, CallerKindUser) is never exercised on the
//     MCP path, because mcpAuthMiddleware never hands the downstream handler
//     an AuthContext of Type == auth.AuthTypeUser — pinned with a counter so
//     the assertion cannot pass vacuously on an empty case list, and proved
//     non-trivial by showing the same cache method DOES report CallerKindUser
//     for a UserContext (the shape a would-be session door would produce).
//
// Green on HEAD by design (PR-B already forces MCP auth); this file exists so
// a future change that reintroduces a session/JWT credential path on /mcp, or
// that starts handing MCP handlers a user-typed AuthContext, fails loudly
// here instead of silently reopening FR-043(a)/FR-003.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
)

// mcpRoutesUnderTest mirrors the route set mounted at server.go:2699-2731 —
// every streamable-HTTP MCP entry point, not just the bare "/mcp" this
// package's other fixtures default to.
var mcpRoutesUnderTest = []string{"/mcp", "/mcp/all", "/mcp/code", "/mcp/call", "/mcp/p/research"}

// TestMCPSessionCredential_NeverReachesAnyMCPRoute is FR-043(a) applied across
// the full route set: a session cookie, a user JWT (as Bearer or X-API-Key),
// and their combination are refused with 401 on every /mcp* route, and no
// credential is refused too once server_edition.enabled forces the effective
// value — even with the raw require_mcp_auth field explicitly false.
func TestMCPSessionCredential_NeverReachesAnyMCPRoute(t *testing.T) {
	srv := newForcedAuthTestServer(t, forcedAuthConfig(t, "test-api-key-t068", false, true), zap.NewNop())
	require.False(t, srv.runtime.Config().RequireMCPAuth,
		"fixture: the configured value stays false; server_edition.enabled must force the effective one")

	jwt := mintUserJWT(t, srv)

	shapes := map[string]func(*http.Request){
		"no credential": func(*http.Request) {},
		"session cookie alone": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: "sess-t068-0123456789"})
		},
		"user JWT as Bearer": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+jwt)
		},
		"user JWT as X-API-Key": func(r *http.Request) {
			r.Header.Set("X-API-Key", jwt)
		},
		"user JWT plus session cookie": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+jwt)
			r.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: "sess-t068-0123456789"})
		},
	}

	checked := 0
	for _, route := range mcpRoutesUnderTest {
		for name, shape := range shapes {
			checked++
			req := httptest.NewRequest(http.MethodPost, route, http.NoBody)
			shape(req)
			seen, code := captureAuthContext(t, srv, req)
			assert.Equal(t, http.StatusUnauthorized, code, "%s on %s", name, route)
			assert.Nil(t, seen, "%s on %s: downstream handler must not run for a refused caller", name, route)
		}
	}

	// Counter fixture: len(routes) * len(shapes) sub-checks actually ran, so
	// the pass above cannot be a vacuous empty loop.
	require.Equal(t, len(mcpRoutesUnderTest)*len(shapes), checked)
}

// TestCacheAuthorizationCallerKindUser_NeverProducedOnMCPPath pins the second
// half of FR-043(a)/FR-003: cache.CallerKindUser (cache/authorization.go:103)
// exists and is reachable from cacheAuthorizationWith, but no AuthContext
// mcpAuthMiddleware can actually hand a downstream MCP handler ever triggers
// it — because none of them carry auth.AuthTypeUser.
//
// A counting stub proves this is not a vacuous pass: it counts, for every
// AuthContext shape mcpAuthMiddleware is capable of producing, how many times
// cacheAuthorizationWith is invoked and how many of those came back
// CallerKindUser (must be zero), and separately proves the gate is live by
// feeding it a genuine auth.UserContext (the shape a session door would
// produce, which nothing on the MCP path emits today) and requiring THAT one
// reports CallerKindUser — so the zero count above is a property of the
// inputs, not of a broken or bypassed switch.
func TestCacheAuthorizationCallerKindUser_NeverProducedOnMCPPath(t *testing.T) {
	p := &MCPProxyServer{}

	// Every AuthContext shape mcpAuthMiddleware is observed to construct
	// (server.go:373-478): the unauthenticated-but-required-off admin
	// fallback, the anonymous admin fallback, the global-API-key admin
	// context, and an agent token's AuthContext. A nil context (auth
	// middleware never installed, e.g. a raw stdio caller) is included too.
	agentAuthCtx := (&auth.AgentToken{Name: "t068-agent", AllowedServers: []string{"*"}, Permissions: []string{"read"}}).AuthContext()

	produced := 0
	callerKindUserCount := 0
	shapes := map[string]*auth.AuthContext{
		"nil (no auth context installed)":              nil,
		"admin (API key)":                              auth.AdminContext(),
		"anonymous admin (no credential, back-compat)": auth.AnonymousContext(),
		"agent token":                                  agentAuthCtx,
	}
	for name, ac := range shapes {
		produced++
		ctx := context.Background()
		if ac != nil {
			ctx = auth.WithAuthContext(ctx, ac)
		}
		got := p.cacheAuthorizationWith(ctx, "", nil)
		if got.CallerKind == cache.CallerKindUser {
			callerKindUserCount++
		}
		assert.NotEqual(t, cache.CallerKindUser, got.CallerKind, "%s must never map to CallerKindUser on the MCP path", name)
	}
	require.Equal(t, len(shapes), produced, "counter fixture: every declared shape must have been exercised")
	require.Equal(t, 0, callerKindUserCount, "no MCP-producible AuthContext maps to CallerKindUser")

	// Proves the gate is real and reachable in principle: a genuine session
	// principal (auth.UserContext — the shape FR-043(a) forbids on /mcp) DOES
	// map to CallerKindUser. If this assertion ever failed, the zero count
	// above would be meaningless (the switch itself broken, not merely unfed).
	sessionCtx := auth.WithAuthContext(context.Background(), auth.UserContext("u-t068", "alice@example.com", "Alice", "google"))
	got := p.cacheAuthorizationWith(sessionCtx, "", nil)
	require.Equal(t, cache.CallerKindUser, got.CallerKind,
		"fixture: auth.UserContext must map to CallerKindUser, or the negative assertions above are vacuous")
}
