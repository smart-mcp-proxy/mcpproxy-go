package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

const coreSentinel = "SENTINEL-alice-7c1d"

type seenReq struct {
	method string
	hdr    http.Header
}

type fwdUpstream struct {
	mu   sync.Mutex
	seen []seenReq
	// callBody, when set, answers tools/call with this raw JSON-RPC result text
	// (echoing the sentinel); otherwise tools/call answers 500 with the echo.
	errorEcho bool
}

func (u *fwdUpstream) reqs(method string) []seenReq {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []seenReq
	for _, r := range u.seen {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
}

func (u *fwdUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		u.mu.Lock()
		u.seen = append(u.seen, seenReq{req.Method, r.Header.Clone()})
		u.mu.Unlock()
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, map[string]any{
				"protocolVersion": req.Params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fwd", "version": "1"},
			})
		case "tools/list":
			writeJSONRPC(w, req.ID, map[string]any{"tools": []any{}})
		case "tools/call":
			if u.errorEcho {
				http.Error(w, "boom: X-Tenant-Id: "+r.Header.Get("X-Tenant-Id")+" "+r.Header.Get("X-Tenant-Id"), http.StatusInternalServerError)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello " + r.Header.Get("X-Tenant-Id")}}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	})
}

func edgeCtx(t *testing.T, headers map[string]string, union ...string) context.Context {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	u := map[string]struct{}{}
	for _, n := range union {
		u[n] = struct{}{}
	}
	return headerfwd.WithSnapshot(context.Background(), headerfwd.Capture(r, u))
}

func newFwdClient(t *testing.T, u *fwdUpstream, mutate func(*config.ServerConfig)) (*Client, *observer.ObservedLogs) {
	t.Helper()
	disableOAuthForTest(t)
	srv := httptest.NewServer(u.handler())
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{
		Name: "fwd", Protocol: "streamable-http", URL: srv.URL, Enabled: true,
		ForwardHeaders: []string{"X-Tenant-Id"},
	}
	if mutate != nil {
		mutate(cfg)
	}
	obsCore, logs := observer.New(zapcore.DebugLevel)
	c, err := NewClient("fwd", cfg, zap.New(obsCore), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	return c, logs
}

func policyFor(cfg *config.ServerConfig, enabled bool) func() headerfwd.Policy {
	return func() headerfwd.Policy {
		return headerfwd.Policy{Enabled: enabled, Allow: cfg.ForwardHeaders, Static: cfg.Headers}
	}
}

func TestCallTool_ForwardsOnlyOnToolsCall(t *testing.T) {
	u := &fwdUpstream{}
	c, _ := newFwdClient(t, u, nil)
	c.SetForwardPolicyProvider(policyFor(c.config, true))

	ctx := edgeCtx(t, map[string]string{"X-Tenant-Id": coreSentinel, "X-Other": "no"}, "X-Tenant-Id")
	res, err := c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)
	require.NotNil(t, res)

	calls := u.reqs("tools/call")
	require.Len(t, calls, 1)
	assert.Equal(t, coreSentinel, calls[0].hdr.Get("X-Tenant-Id"))
	assert.Empty(t, calls[0].hdr.Get("X-Other"), "only allowlisted names")

	// key A alone in ctx of a connect/list must never forward.
	_, _ = c.ListTools(ctx)
	for _, m := range []string{"initialize", "tools/list", "notifications/initialized"} {
		for _, r := range u.reqs(m) {
			assert.Empty(t, r.hdr.Get("X-Tenant-Id"), m+" must not forward")
		}
	}
}

func TestCallTool_NoProviderOrDisabledForwardsNothing(t *testing.T) {
	u := &fwdUpstream{}
	c, _ := newFwdClient(t, u, nil)
	ctx := edgeCtx(t, map[string]string{"X-Tenant-Id": coreSentinel}, "X-Tenant-Id")

	_, err := c.CallTool(ctx, "echo", nil) // no provider: fail closed
	require.NoError(t, err)

	c.SetForwardPolicyProvider(policyFor(c.config, false)) // global switch off
	_, err = c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)

	// Live flip without reconnect: enabled now.
	enabled := true
	c.SetForwardPolicyProvider(func() headerfwd.Policy {
		return headerfwd.Policy{Enabled: enabled, Allow: []string{"X-Tenant-Id"}}
	})
	_, err = c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)
	// Allowlist edit takes effect at once as well.
	c.SetForwardPolicyProvider(func() headerfwd.Policy { return headerfwd.Policy{Enabled: true} })
	_, err = c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)

	calls := u.reqs("tools/call")
	require.Len(t, calls, 4)
	assert.Empty(t, calls[0].hdr.Get("X-Tenant-Id"))
	assert.Empty(t, calls[1].hdr.Get("X-Tenant-Id"))
	assert.Equal(t, coreSentinel, calls[2].hdr.Get("X-Tenant-Id"))
	assert.Empty(t, calls[3].hdr.Get("X-Tenant-Id"))
	assert.Len(t, u.reqs("initialize"), 1, "no reconnect happened")
}

func TestCallTool_StaticHeaderWins(t *testing.T) {
	u := &fwdUpstream{}
	c, _ := newFwdClient(t, u, func(cfg *config.ServerConfig) {
		cfg.Headers = map[string]string{"X-Tenant-Id": "operator"}
		cfg.ForwardHeaders = []string{"X-Tenant-Id"}
	})
	c.SetForwardPolicyProvider(policyFor(c.config, true))
	ctx := edgeCtx(t, map[string]string{"X-Tenant-Id": coreSentinel}, "X-Tenant-Id")
	_, err := c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)
	calls := u.reqs("tools/call")
	require.Len(t, calls, 1)
	assert.Equal(t, "operator", calls[0].hdr.Get("X-Tenant-Id"))
}

func TestCallTool_ScrubsErrorAndRecordsSink(t *testing.T) {
	u := &fwdUpstream{errorEcho: true}
	c, logs := newFwdClient(t, u, nil)
	c.SetForwardPolicyProvider(policyFor(c.config, true))
	ctx := edgeCtx(t, map[string]string{"X-Tenant-Id": coreSentinel}, "X-Tenant-Id")
	ctx, sink := headerfwd.WithSink(ctx)

	_, err := c.CallTool(ctx, "echo", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), coreSentinel, "error must be scrubbed before it leaves CallTool")
	assert.Contains(t, err.Error(), "[forwarded:X-Tenant-Id]")

	assert.Equal(t, []string{"X-Tenant-Id"}, sink.Outbound().Names())

	var all strings.Builder
	for _, e := range logs.All() {
		all.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			all.WriteString(" " + k + "=")
			if s, ok := v.(string); ok {
				all.WriteString(s)
			}
		}
	}
	assert.NotContains(t, all.String(), coreSentinel, "no value in logs")
}

func TestOutbound_SSEAndStdioNeverForward(t *testing.T) {
	// The policy the core applies is keyed on ITS resolved transport type, so a
	// provider claiming an http transport cannot make an sse client forward.
	snap := headerfwd.Capture(func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		r.Header.Set("X-Tenant-Id", coreSentinel)
		return r
	}(), map[string]struct{}{"X-Tenant-Id": {}})
	for _, tr := range []string{"sse", "stdio", ""} {
		out := headerfwd.Outbound(snap, headerfwd.Policy{Enabled: true, Allow: []string{"X-Tenant-Id"}, Transport: tr})
		assert.True(t, out.IsEmpty(), "transport %q", tr)
	}
}
