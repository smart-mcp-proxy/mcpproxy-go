package managed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

const mgdSentinel = "SENTINEL-bob-42e0"

type mgdUpstream struct {
	mu    sync.Mutex
	calls []http.Header
	inits int
}

func (u *mgdUpstream) handler() http.Handler {
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
		write := func(res map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": res})
		}
		switch req.Method {
		case "initialize":
			u.mu.Lock()
			u.inits++
			u.mu.Unlock()
			write(map[string]any{"protocolVersion": req.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "m", "version": "1"}})
		case "tools/call":
			u.mu.Lock()
			u.calls = append(u.calls, r.Header.Clone())
			u.mu.Unlock()
			write(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			write(map[string]any{"tools": []any{}})
		}
	})
}

func TestManagedForwardPolicy_LiveFromAtomicConfigs(t *testing.T) {
	cfg := &config.ServerConfig{Name: "s", Protocol: "streamable-http", URL: "http://127.0.0.1:1/mcp",
		ForwardHeaders: []string{"X-Tenant-Id"}, Headers: map[string]string{"X-Static": "v"}}
	mc, err := NewClient("s", cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)

	p := mc.forwardPolicy()
	assert.True(t, p.Enabled)
	assert.Equal(t, []string{"X-Tenant-Id"}, p.Allow)
	assert.Contains(t, p.Static, "X-Static")

	off := false
	mc.SetGlobalConfig(&config.Config{ForwardClientHeaders: &off})
	assert.False(t, mc.forwardPolicy().Enabled)

	next := config.CopyServerConfig(cfg)
	next.ForwardHeaders = []string{"X-Region"}
	mc.SetConfig(next)
	assert.Equal(t, []string{"X-Region"}, mc.forwardPolicy().Allow)

	t.Setenv(config.EnvForwardClientHeaders, "off")
	mc.SetGlobalConfig(&config.Config{})
	assert.False(t, mc.forwardPolicy().Enabled, "env kill switch wins")
}

func TestManagedCallTool_HotReloadNoReconnect(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	u := &mgdUpstream{}
	srv := httptest.NewServer(u.handler())
	defer srv.Close()

	cfg := &config.ServerConfig{Name: "s", Protocol: "streamable-http", URL: srv.URL, Enabled: true,
		ForwardHeaders: []string{"X-Tenant-Id"}}
	mc, err := NewClient("s", cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, mc.Connect(ctx))
	t.Cleanup(func() { _ = mc.Disconnect() })

	r := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	r.Header.Set("X-Tenant-Id", mgdSentinel)
	r.Header.Set("X-Region", "eu")
	edge := headerfwd.WithSnapshot(context.Background(),
		headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}, "X-Region": {}}))

	call := func() http.Header {
		_, err := mc.CallTool(edge, "echo", nil)
		require.NoError(t, err)
		u.mu.Lock()
		defer u.mu.Unlock()
		return u.calls[len(u.calls)-1]
	}

	h := call()
	assert.Equal(t, mgdSentinel, h.Get("X-Tenant-Id"))
	assert.Empty(t, h.Get("X-Region"))

	next := config.CopyServerConfig(cfg)
	next.ForwardHeaders = []string{"X-Region"} // allowlist edit
	mc.SetConfig(next)
	h = call()
	assert.Empty(t, h.Get("X-Tenant-Id"))
	assert.Equal(t, "eu", h.Get("X-Region"))

	off := false
	mc.SetGlobalConfig(&config.Config{ForwardClientHeaders: &off}) // switch flip
	h = call()
	assert.Empty(t, h.Get("X-Region"))

	u.mu.Lock()
	defer u.mu.Unlock()
	assert.Equal(t, 1, u.inits, "hot reload must not reconnect")
}

func TestManagedWarnForwardHeaders_OnceNamesOnly(t *testing.T) {
	obsCore, logs := observer.New(zapcore.WarnLevel)
	mk := func(cfg *config.ServerConfig) *Client {
		mc, err := NewClient(cfg.Name, cfg, zap.New(obsCore), nil, &config.Config{}, nil, secret.NewResolver())
		require.NoError(t, err)
		return mc
	}
	// stdio with an allowlist: inert, warned once.
	stdio := mk(&config.ServerConfig{Name: "stdio1", Protocol: "stdio", Command: "true", ForwardHeaders: []string{"X-Tenant-Id"}})
	stdio.warnForwardHeaders()
	stdio.SetConfig(config.CopyServerConfig(stdio.GetConfig()))
	// plain-HTTP non-loopback: warned once.
	plain := mk(&config.ServerConfig{Name: "plain1", Protocol: "streamable-http", URL: "http://upstream.example.com/mcp", ForwardHeaders: []string{"X-Tenant-Id"}})
	plain.warnForwardHeaders()
	// loopback plain HTTP, https, and empty allowlist: silent.
	mk(&config.ServerConfig{Name: "lo", Protocol: "streamable-http", URL: "http://127.0.0.1:9/mcp", ForwardHeaders: []string{"X-Tenant-Id"}})
	mk(&config.ServerConfig{Name: "tls", Protocol: "streamable-http", URL: "https://upstream.example.com/mcp", ForwardHeaders: []string{"X-Tenant-Id"}})
	mk(&config.ServerConfig{Name: "empty", Protocol: "streamable-http", URL: "http://upstream.example.com/mcp"})

	counts := map[string]int{}
	for _, e := range logs.All() {
		counts[e.ContextMap()["server"].(string)]++
	}
	assert.Equal(t, map[string]int{"stdio1": 1, "plain1": 1}, counts)
}
