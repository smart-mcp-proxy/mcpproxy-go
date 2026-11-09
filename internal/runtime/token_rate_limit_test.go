package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// TestRecordToolCallRejected_RateLimitedNamesTheToken: a rate_limited record is
// about one agent token, so it carries the token's name and prefix where a
// concurrency shed carries no caller (issue #1699).
func TestRecordToolCallRejected_RateLimitedNamesTheToken(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.RecordToolCallRejected(Event{
		Type:      EventTypeActivityToolCallRejected,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name":    "github",
			"tool_name":      "create_issue",
			"source":         "mcp",
			"request_id":     "req-7",
			"reason":         "rate_limited",
			"scope":          "token",
			"message":        "rate limit reached",
			"limit":          60,
			"retry_after_ms": int64(1000),
			"attribution": ActivityAttribution{
				TokenName: "loop-guard", TokenPrefix: "mcp_agt_ab12", ClientID: "cursor",
			}.payload(),
		},
	})

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)
	rec := records[0]
	assert.Equal(t, storage.ActivityTypeToolCall, rec.Type)
	assert.Equal(t, storage.ActivityStatusRejected, rec.Status, "a refused call is a rejected tool_call, not an authz line")
	assert.Equal(t, "rate_limited", rec.Metadata[storage.MetadataKeyRejectionReason])
	assert.Equal(t, "token", rec.Metadata[storage.MetadataKeyRejectionScope])
	assert.EqualValues(t, 60, toInt64(rec.Metadata[storage.MetadataKeyRejectionLimit]))
	assert.EqualValues(t, 1000, toInt64(rec.Metadata[storage.MetadataKeyRejectionRetryAfterMs]))
	assert.Equal(t, "loop-guard", rec.TokenName)
	assert.Equal(t, "mcp_agt_ab12", rec.TokenPrefix)
	assert.Equal(t, "cursor", rec.ClientID)
	assert.Equal(t, "proxy_policy", rec.ErrorClass)
	assert.Equal(t, "proxy", rec.FaultDomain)
}

// TestRecordToolCallRejected_ConcurrencyShedStaysUnattributed pins the
// behaviour that did not change: a queue shed names no caller.
func TestRecordToolCallRejected_ConcurrencyShedStaysUnattributed(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.RecordToolCallRejected(Event{
		Type:      EventTypeActivityToolCallRejected,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "db", "tool_name": "query", "reason": "queue_full", "scope": "server",
		},
	})

	records, _, err := store.ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Empty(t, records[0].TokenName)
	assert.Empty(t, records[0].TokenPrefix)
}

// startEchoUpstream boots an in-process streamable-HTTP MCP server with one
// instant tool and counts how many calls actually reach it.
func startEchoUpstream(t *testing.T) (addr string, reached *atomic.Int64) {
	t.Helper()
	reached = &atomic.Int64{}

	srv := mcpserver.NewMCPServer("echo", "1.0.0-test", mcpserver.WithToolCapabilities(true))
	srv.AddTool(mcp.Tool{
		Name:        "ping",
		Description: "Answers at once",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{}},
	}, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reached.Add(1)
		return mcp.NewToolResultText("pong"), nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(srv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", ln.Addr().String()), reached
}

// TestAgentTokenRateLimit_EndToEnd drives a real runtime and a live upstream:
// the config default limits an agent token at the managed-client seam, the
// refusal is a typed error that never reaches the upstream, it is recorded as
// one rejected tool_call naming the token and counted by the rejection metric,
// and the admin caller and other tokens are unaffected. A hot reload that
// turns the limit off takes effect on the next call.
func TestAgentTokenRateLimit_EndToEnd(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	rt := newTestRuntime(t)
	addr, reached := startEchoUpstream(t)

	um := rt.UpstreamManager()
	srvCfg := &config.ServerConfig{Name: "echo", URL: addr, Protocol: "streamable-http", Enabled: true}
	require.NoError(t, um.AddServerConfig("echo", srvCfg))
	require.NoError(t, um.ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		c, ok := um.GetClient("echo")
		return ok && c.IsConnected()
	}, 15*time.Second, 50*time.Millisecond, "the upstream must connect")

	var metricMu sync.Mutex
	var metrics [][3]string
	rt.SetRejectionMetricSink(func(server, reason, scope string) {
		metricMu.Lock()
		defer metricMu.Unlock()
		metrics = append(metrics, [3]string{server, reason, scope})
	})

	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	um.Limiters().TokenRates().SetClock(func() time.Time { return now })

	cfg := *um.GlobalConfig()
	cfg.AgentTokenRateLimit = &config.AgentTokenRateLimit{PerMinute: 60, Burst: 2}
	um.SetGlobalConfig(&cfg)

	call := func(ctx context.Context) error {
		ctx = reqcontext.WithRequestSource(ctx, reqcontext.SourceMCP)
		_, err := um.CallTool(ctx, "echo:ping", map[string]interface{}{})
		return err
	}
	tokenCtx := func(name string, rate *auth.TokenRateLimit) context.Context {
		tok := &auth.AgentToken{Name: name, TokenPrefix: "mcp_agt_" + name[:2] + "00", RateLimit: rate}
		return auth.WithAuthContext(context.Background(), tok.AuthContext())
	}

	agent := tokenCtx("looper", nil)
	require.NoError(t, call(agent))
	require.NoError(t, call(agent))
	err := call(agent)
	require.Error(t, err)
	require.True(t, errors.Is(err, limiter.ErrRateLimited), "got %v", err)
	var limitErr *limiter.LimitError
	require.True(t, errors.As(err, &limitErr))
	assert.Equal(t, time.Second, limitErr.RetryAfter)
	assert.EqualValues(t, 2, reached.Load(), "the refused call never reached the upstream")

	// The rejected tool_call is recorded once, synchronously, naming the token.
	records, _, lerr := rt.StorageManager().ListActivities(storage.DefaultActivityFilter())
	require.NoError(t, lerr)
	var rejected []*storage.ActivityRecord
	for _, r := range records {
		if r.Status == storage.ActivityStatusRejected {
			rejected = append(rejected, r)
		}
	}
	require.Len(t, rejected, 1)
	assert.Equal(t, storage.ActivityTypeToolCall, rejected[0].Type)
	assert.Equal(t, "echo", rejected[0].ServerName)
	assert.Equal(t, "ping", rejected[0].ToolName)
	assert.Equal(t, storage.ActivitySource("mcp"), rejected[0].Source)
	assert.Equal(t, "rate_limited", rejected[0].Metadata[storage.MetadataKeyRejectionReason])
	assert.Equal(t, "token", rejected[0].Metadata[storage.MetadataKeyRejectionScope])
	assert.Equal(t, "looper", rejected[0].TokenName)
	assert.Equal(t, "mcp_agt_lo00", rejected[0].TokenPrefix)

	metricMu.Lock()
	assert.Equal(t, [][3]string{{"echo", "rate_limited", "token"}}, metrics)
	metricMu.Unlock()

	// Another token, the admin API key and a caller with no identity are not
	// affected by looper's spent budget.
	require.NoError(t, call(tokenCtx("other", nil)))
	require.NoError(t, call(auth.WithAuthContext(context.Background(), auth.AdminContext())))
	require.NoError(t, call(auth.WithAuthContext(context.Background(), auth.AnonymousContext())))
	require.NoError(t, call(context.Background()))
	assert.EqualValues(t, 6, reached.Load())

	// A token's own rate_limit replaces the default, in both directions.
	roomy := tokenCtx("roomy", &auth.TokenRateLimit{PerMinute: 60, Burst: 4})
	for i := 0; i < 4; i++ {
		require.NoError(t, call(roomy), "roomy's own burst of 4 applies")
	}
	require.True(t, errors.Is(call(roomy), limiter.ErrRateLimited))
	exempt := tokenCtx("exempt", &auth.TokenRateLimit{PerMinute: 0})
	for i := 0; i < 5; i++ {
		require.NoError(t, call(exempt), "per_minute 0 exempts a token from the default")
	}

	// Time refills the bucket.
	now = now.Add(time.Second)
	require.NoError(t, call(agent))
	require.True(t, errors.Is(call(agent), limiter.ErrRateLimited))

	// Hot reload: turning the limit off takes effect on the next call.
	cfg.AgentTokenRateLimit = nil
	um.SetGlobalConfig(&cfg)
	for i := 0; i < 10; i++ {
		require.NoError(t, call(agent))
	}
}
