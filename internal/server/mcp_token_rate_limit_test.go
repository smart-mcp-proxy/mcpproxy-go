package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// Issue #1699: a per-agent-token call budget enforced at the shared managed-
// client admission seam. These tests drive the real dispatch paths (the
// call_tool_* variants, the code_execution sandbox, direct routing mode and the
// REST tool-call entry point) against a live counting upstream, so what they
// witness is the number of calls that actually reached it.

type rateLimitFixture struct {
	proxy *MCPProxyServer
	up    *countingUpstream
	now   *time.Time
	mu    sync.Mutex
	seen  []limiter.Rejection
}

func newRateLimitFixture(t *testing.T, def limiter.RateSpec) *rateLimitFixture {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

	f := &rateLimitFixture{proxy: proxy, up: up}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f.now = &now
	// The proxy's manager has no runtime observer in this fixture; this one
	// stands in for it so the test sees what the seam reported.
	proxy.upstreamManager.SetRejectionObserver(func(_ context.Context, rej limiter.Rejection) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.seen = append(f.seen, rej)
	})
	rates := proxy.upstreamManager.Limiters().TokenRates()
	rates.SetClock(func() time.Time { return now })
	rates.SetDefault(def)
	return f
}

func (f *rateLimitFixture) rejections() []limiter.Rejection {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]limiter.Rejection(nil), f.seen...)
}

// namedAgent is a full-tier agent token on server "a" with its own name, so
// each test token has its own bucket.
func namedAgent(name string, rate *auth.TokenRateLimit) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      name,
		TokenPrefix:    "mcp_agt_" + name[:2] + "00",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		RateLimit:      rate,
	})
}

func TestCallToolRead_AgentTokenRateLimit(t *testing.T) {
	f := newRateLimitFixture(t, limiter.RateSpec{PerMinute: 60, Burst: 2})
	agent := namedAgent("looper", nil)

	for i := 0; i < 2; i++ {
		res, text := callToolReadResult(t, f.proxy, agent, "a:erase")
		assert.False(t, res.IsError, "call %d is inside the burst: %s", i+1, text)
	}
	assert.Equal(t, int64(2), f.up.count.Load())

	res, text := callToolReadResult(t, f.proxy, agent, "a:erase")
	require.True(t, res.IsError, "the third call is over the budget")
	assert.Contains(t, text, "Rate limit reached")
	assert.Contains(t, text, "60 tool calls per minute")
	assert.Contains(t, text, "Retry in 1 second.")
	assert.Equal(t, int64(2), f.up.count.Load(), "the refused call never reached the upstream")

	seen := f.rejections()
	require.Len(t, seen, 1)
	assert.Equal(t, limiter.ReasonRateLimited, seen[0].Reason)
	assert.Equal(t, limiter.ScopeToken, seen[0].Scope)
	assert.Equal(t, "looper", seen[0].Token.Name)
	assert.Equal(t, "a", seen[0].Server)
	assert.Equal(t, "erase", seen[0].Tool)

	// One token's spent budget leaves everyone else alone: another token, the
	// API-key administrator, and a caller with no identity.
	for label, ctx := range map[string]context.Context{
		"another token":     namedAgent("other", nil),
		"admin":             adminCtx(),
		"anonymous":         auth.WithAuthContext(context.Background(), auth.AnonymousContext()),
		"no auth context":   context.Background(),
		"token's own limit": namedAgent("roomy", &auth.TokenRateLimit{PerMinute: 60, Burst: 5}),
	} {
		res, text := callToolReadResult(t, f.proxy, ctx, "a:erase")
		assert.False(t, res.IsError, "%s must not be limited: %s", label, text)
	}

	// The budget refills with time.
	*f.now = f.now.Add(time.Second)
	res, text = callToolReadResult(t, f.proxy, agent, "a:erase")
	assert.False(t, res.IsError, "a second later one call has refilled: %s", text)
}

func TestCallToolRead_NoRateLimitByDefault(t *testing.T) {
	f := newRateLimitFixture(t, limiter.RateSpec{})
	agent := namedAgent("looper", nil)
	for i := 0; i < 25; i++ {
		res, text := callToolReadResult(t, f.proxy, agent, "a:erase")
		require.False(t, res.IsError, "call %d: %s", i+1, text)
	}
	assert.Empty(t, f.rejections())
}

func TestCodeExecution_AgentTokenRateLimit(t *testing.T) {
	f := newRateLimitFixture(t, limiter.RateSpec{PerMinute: 60, Burst: 2})
	agent := namedAgent("scripted", nil)

	for i := 0; i < 2; i++ {
		call := runSandboxCallTool(t, f.proxy, agent, "a", "erase")
		require.True(t, call.OK, "call %d: %q %s", i+1, call.Code, call.Message)
	}
	call := runSandboxCallTool(t, f.proxy, agent, "a", "erase")
	assert.False(t, call.OK, "a script's nested calls spend the same token's budget")
	assert.Contains(t, call.Message, "Rate limit reached")
	assert.Equal(t, int64(2), f.up.count.Load())

	admin := runSandboxCallTool(t, f.proxy, adminCtx(), "a", "erase")
	assert.True(t, admin.OK, "the administrator's scripts are not limited: %q %s", admin.Code, admin.Message)
}

func TestDirectMode_AgentTokenRateLimit(t *testing.T) {
	f := newRateLimitFixture(t, limiter.RateSpec{PerMinute: 60, Burst: 1})
	entry := &directCatalogEntry{
		ServerName:  "a",
		ToolName:    "erase",
		DisplayName: FormatDirectToolName("a", "erase"),
		ParamsJSON:  `{"type":"object"}`,
		Annotations: readSpec("erase").Annotations,
	}
	call := func(ctx context.Context) *mcp.CallToolResult {
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := f.proxy.makeDirectModeHandler(entry)(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		return result
	}
	agent := namedAgent("direct", nil)

	require.False(t, call(agent).IsError)
	res := call(agent)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(mcp.TextContent).Text, "Rate limit reached")
	assert.Equal(t, int64(1), f.up.count.Load())
	assert.False(t, call(adminCtx()).IsError)
}

// TestCallToolDirect_RateLimitKeepsTheTypedIdentityForREST: the REST tool-call
// route answers a refusal with 429 + Retry-After, which needs the typed
// *limiter.LimitError to survive the dispatch's isError result.
func TestCallToolDirect_RateLimitKeepsTheTypedIdentityForREST(t *testing.T) {
	f := newRateLimitFixture(t, limiter.RateSpec{PerMinute: 60, Burst: 1})
	agent := namedAgent("rest", nil)

	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{"name": "a:erase"}

	_, err := f.proxy.CallToolDirectResult(agent, req)
	require.NoError(t, err)

	_, err = f.proxy.CallToolDirectResult(agent, req)
	require.Error(t, err)
	var limitErr *limiter.LimitError
	require.True(t, errors.As(err, &limitErr), "got %T: %v", err, err)
	assert.Equal(t, limiter.ReasonRateLimited, limitErr.Reason)
	assert.Equal(t, time.Second, limitErr.RetryAfter)
	assert.Equal(t, int64(1), f.up.count.Load())

	_, err = f.proxy.CallToolDirectResult(adminCtx(), req)
	require.NoError(t, err, "the REST admin path is unaffected")
}
