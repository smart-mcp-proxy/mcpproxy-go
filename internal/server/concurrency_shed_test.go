package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func shedIntPtr(v int) *int { return &v }

func shedDurPtr(d time.Duration) *config.Duration {
	cd := config.Duration(d)
	return &cd
}

// TestShedMessage_ServerScopeNamesServerAndLimit covers FR-010's per-server
// wording: the message identifies the server, its cap, why it was shed, and
// tells the agent to retry.
func TestShedMessage_ServerScopeNamesServerAndLimit(t *testing.T) {
	msg := shedMessage(&limiter.LimitError{
		Scope:  limiter.ScopeServer,
		Reason: limiter.ReasonQueueFull,
		Server: "analytics-db",
		Limit:  2,
	})

	assert.Contains(t, msg, `"analytics-db"`)
	assert.Contains(t, msg, "2")
	assert.Contains(t, msg, "wait queue is full")
	assert.Contains(t, msg, limiter.RetryAdvice)
}

// TestShedMessage_GlobalScopeNeverBlamesAServer covers FR-010's explicit rule.
func TestShedMessage_GlobalScopeNeverBlamesAServer(t *testing.T) {
	msg := shedMessage(&limiter.LimitError{
		Scope:  limiter.ScopeGlobal,
		Reason: limiter.ReasonQueueTimeout,
		Server: "", // a global rejection carries no server
		Limit:  20,
	})

	assert.Contains(t, msg, "proxy-wide")
	assert.Contains(t, msg, "20")
	assert.Contains(t, msg, "queue timeout")
	assert.Contains(t, msg, limiter.RetryAdvice)
	assert.NotContains(t, strings.ToLower(msg), "server \"")
}

// TestShedToolResult_IsErrorResultNotProtocolError covers FR-010: an agent must
// receive a readable tool-call error, not a transport failure.
func TestShedToolResult_IsErrorResultNotProtocolError(t *testing.T) {
	res := shedToolResult(&limiter.LimitError{
		Scope:  limiter.ScopeServer,
		Reason: limiter.ReasonQueueFull,
		Server: "db",
		Limit:  1,
	})

	require.NotNil(t, res)
	assert.True(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "db")
	assert.Contains(t, text.Text, limiter.RetryAdvice)
}

// TestAsShed_ServerUnavailableIsNotAShed keeps the FR-009 semantics separate:
// a server disabled/removed while a call was queued is "server unavailable",
// not backpressure.
func TestAsShed_ServerUnavailableIsNotAShed(t *testing.T) {
	_, ok := asShed(&limiter.LimitError{Scope: limiter.ScopeServer, Reason: limiter.ReasonServerUnavailable, Server: "db"})
	assert.False(t, ok)

	_, ok = asShed(errors.New("some upstream failure"))
	assert.False(t, ok)

	_, ok = asShed(nil)
	assert.False(t, ok)
}

// TestShedCapture_PreservesTypedIdentity covers the FR-011 side channel: the
// MCP handlers can only answer with an isError result, so the typed rejection
// has to reach CallToolDirect some other way.
func TestShedCapture_PreservesTypedIdentity(t *testing.T) {
	ctx, box := withShedCapture(context.Background())
	assert.Nil(t, box.take(), "nothing captured before a shed")

	limitErr := &limiter.LimitError{Scope: limiter.ScopeServer, Reason: limiter.ReasonQueueTimeout, Server: "db", Limit: 3, RetryAfter: 7 * time.Second}
	recordShed(ctx, limitErr)

	got := box.take()
	require.NotNil(t, got)
	assert.Equal(t, limitErr, got)

	// A dispatch with no capture box installed (the plain MCP transport path)
	// must not panic.
	recordShed(context.Background(), limitErr)
}

// TestShedDispatchError_UnwrapsToLimitError proves the REST handler's
// errors.As(...) will see the typed rejection through the wrapper.
func TestShedDispatchError_UnwrapsToLimitError(t *testing.T) {
	limitErr := &limiter.LimitError{Scope: limiter.ScopeGlobal, Reason: limiter.ReasonQueueFull, Limit: 5, RetryAfter: 2 * time.Second}
	err := error(&shedDispatchError{limitErr: limitErr, message: shedMessage(limitErr)})

	var target *limiter.LimitError
	require.True(t, errors.As(err, &target))
	assert.Equal(t, limiter.ScopeGlobal, target.Scope)
	assert.Equal(t, 2*time.Second, target.RetryAfter)
	assert.Contains(t, err.Error(), "proxy-wide")
}

// TestCodeExecutionToolCaller_IsSubjectToAdmission is the FR-003 coverage proof
// for the sandboxed code-execution path: it dispatches through the managed
// client like every other origin, so the same limits bound it — even though it
// never traverses handleCallToolVariant.
func TestCodeExecutionToolCaller_IsSubjectToAdmission(t *testing.T) {
	t.Setenv("CI", "")

	serverCfg := &config.ServerConfig{
		Name:                  "db",
		URL:                   "http://127.0.0.1:1",
		Protocol:              "http",
		Enabled:               true,
		MaxConcurrentRequests: shedIntPtr(1),
		QueueSize:             shedIntPtr(0), // no pending capacity: shed at the cap
		QueueTimeout:          shedDurPtr(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}

	um := upstream.NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	require.NoError(t, um.AddServerConfig("db", serverCfg))
	client, ok := um.GetClient("db")
	require.True(t, ok)
	client.StateManager.TransitionTo(types.StateConnecting)
	client.StateManager.TransitionTo(types.StateReady)

	lim := um.Limiters().Server("db")
	require.NotNil(t, lim)
	release, err := lim.Acquire(context.Background(), time.Time{})
	require.NoError(t, err)
	defer release()

	caller := &upstreamToolCaller{
		upstreamManager: um,
		logger:          zap.NewNop(),
		executionID:     "exec-1",
	}

	_, callErr := caller.CallTool(context.Background(), "db", "query", map[string]interface{}{})
	require.Error(t, callErr)
	assert.True(t, errors.Is(callErr, limiter.ErrQueueFull),
		"code_execution must be bounded by the same limiter, got: %v", callErr)
}
