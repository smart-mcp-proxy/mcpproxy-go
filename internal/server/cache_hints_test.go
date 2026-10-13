package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 058 FR-019 / Spec 113-f: list and read results must carry ttlMs and a
// cacheScope. Every mcpproxy listing is caller-dependent (profile, agent-token
// scope, quarantine state), so the scope must be private and the TTL short.
//
// The production client-facing transport is pinned to the legacy era
// (FR-028), where the hints are not emitted, so this test drives a server built
// with the production hint options over an unpinned transport.

func cacheHintTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	opts := append([]mcpserver.ServerOption{
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, true),
		mcpserver.WithPromptCapabilities(true),
	}, cacheHintServerOptions()...)
	srv := mcpserver.NewMCPServer("cache-hint-probe", "1.0.0", opts...)
	srv.AddTool(mcp.NewTool("probe"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(hs.Close)
	return hs
}

func postModernCacheProbe(t *testing.T, url string, method mcp.MCPMethod, params map[string]any) map[string]any {
	t.Helper()
	params["_meta"] = map[string]any{
		mcp.MetaKeyProtocolVersion:    mcp.ProtocolVersion20260728,
		mcp.MetaKeyClientInfo:         map[string]any{"name": "t", "version": "1"},
		mcp.MetaKeyClientCapabilities: map[string]any{},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	req.Header.Set(mcp.HeaderMethod, string(method))
	raw, _ := json.Marshal(params)
	if name, ok := mcp.ExtractHeaderName(method, raw); ok {
		enc, ok := mcp.EncodeHeaderValue(name)
		require.True(t, ok)
		req.Header.Set(mcp.HeaderName, enc)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var msg map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&msg))
	result, ok := msg["result"].(map[string]any)
	require.True(t, ok, "expected result for %s, got %v", method, msg)
	return result
}

func TestListResultsCarryPrivateShortCacheHints(t *testing.T) {
	hs := cacheHintTestServer(t)

	for _, method := range []mcp.MCPMethod{
		mcp.MethodToolsList,
		mcp.MethodPromptsList,
		mcp.MethodResourcesList,
		mcp.MethodResourcesTemplatesList,
	} {
		t.Run(string(method), func(t *testing.T) {
			result := postModernCacheProbe(t, hs.URL, method, map[string]any{})
			assert.Equal(t, string(mcp.CacheScopePrivate), result["cacheScope"],
				"mcpproxy listings depend on profile/token scope, so they must never be public")
			assert.Equal(t, float64(mcpListCacheTTLMs), result["ttlMs"])
		})
	}
}

func TestCacheHintTTLIsShort(t *testing.T) {
	assert.Positive(t, mcpListCacheTTLMs)
	assert.LessOrEqual(t, mcpListCacheTTLMs, int64(10_000),
		"listings change with quarantine/profile state; keep the freshness window conservative")
}

// TestRoutingModeServersCarryCacheHints pins that the real servers behind
// /mcp, /mcp/all, /mcp/code and /mcp/call (not just p.server) advertise the
// private hints. Served over an unpinned transport because the production
// transport is pinned to the legacy era (FR-028).
func TestRoutingModeServersCarryCacheHints(t *testing.T) {
	proxy, _ := newStoredScriptProxyCfg(t, nil)
	for _, mode := range []string{
		config.RoutingModeRetrieveTools,
		config.RoutingModeCodeExecution,
		config.RoutingModeDirect,
	} {
		t.Run(mode, func(t *testing.T) {
			srv := proxy.GetMCPServerForMode(mode)
			require.NotNil(t, srv)
			hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
			t.Cleanup(hs.Close)
			result := postModernCacheProbe(t, hs.URL, mcp.MethodToolsList, map[string]any{})
			assert.Equal(t, string(mcp.CacheScopePrivate), result["cacheScope"])
			assert.Equal(t, float64(mcpListCacheTTLMs), result["ttlMs"])
		})
	}
}
