package server

// Spec 113-c SC-004 / T169: a real proxy in front of an httptest Streamable
// HTTP upstream that produces each failure class on demand. The persisted
// activity record and the REST JSON must carry the expected error_class,
// fault_domain and upstream_http_status.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// newTaxonomyUpstream serves one tool per failure class. The wrapper breaks
// tools/call for the scripted tools only, so initialize and tools/list work.
func newTaxonomyUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcpserver.NewMCPServer("taxonomy-upstream", "1.0.0", mcpserver.WithToolCapabilities(true))
	noArgs := mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}}
	for _, name := range []string{"ok", "http502", "rpc32001", "reset", "slow", "session404", "unauthorized"} {
		srv.AddTool(mcp.Tool{Name: name, Description: "taxonomy tool " + name, InputSchema: noArgs},
			func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("fine"), nil
			})
	}
	srv.AddTool(mcp.Tool{Name: "iserr", Description: "answers isError", InputSchema: noArgs},
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("business failure: nope"), nil
		})
	srv.AddTool(mcp.Tool{Name: "needs_arg", Description: "requires x", InputSchema: mcp.ToolInputSchema{
		Type: "object", Properties: map[string]interface{}{"x": map[string]interface{}{"type": "string"}}, Required: []string{"x"},
	}}, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("fine"), nil
	})

	inner := mcpserver.NewStreamableHTTPServer(srv)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		if msg.Method == "tools/call" {
			switch msg.Params.Name {
			case "http502":
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
				return
			case "rpc32001":
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32001,"message":"custom upstream failure"}}`, msg.ID)
				return
			case "reset":
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, err := hj.Hijack()
					if err == nil {
						_ = conn.Close()
						return
					}
				}
			case "slow":
				select {
				case <-r.Context().Done():
				case <-time.After(15 * time.Second):
				}
				return
			case "session404":
				w.WriteHeader(http.StatusNotFound)
				return
			case "unauthorized":
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		// Replay the consumed body for the real server.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	return hs
}

func TestCallErrorTaxonomy_PersistedAndREST(t *testing.T) {
	upstream := newTaxonomyUpstream(t)

	cases := []struct {
		server     string
		tool       string
		args       map[string]interface{}
		wantClass  string
		wantDomain string
		wantStatus int
	}{
		{"s_toolerr", "iserr", nil, "tool_error", "upstream", 0},
		{"s_http", "http502", nil, "http", "upstream", 502},
		{"s_rpc", "rpc32001", nil, "jsonrpc", "upstream", 0},
		{"s_reset", "reset", nil, "network", "upstream", 0},
		{"s_slow", "slow", nil, "timeout", "upstream", 0},
		{"s_session", "session404", nil, "session_terminated", "upstream", 404},
		{"s_auth", "unauthorized", nil, "auth", "upstream", 401},
		{"s_valid", "needs_arg", map[string]interface{}{}, "proxy_policy", "client", 0},
		{"s_ok", "ok", nil, "", "", 0},
	}
	var servers []*config.ServerConfig
	for _, c := range cases {
		servers = append(servers, fwdServerCfg(c.server, upstream.URL))
	}
	env := startFwdEnv(t, servers, func(cfg *config.Config, _ string) {
		cfg.CallToolTimeout = config.Duration(1500 * time.Millisecond)
	})
	rt := env.proxyServer.runtime
	mcpClient := env.fwdClient("/mcp", nil)

	for _, tc := range cases {
		t.Run(tc.wantClass+"/"+tc.tool, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			req := mcp.CallToolRequest{}
			req.Params.Name = "call_tool_read"
			args := tc.args
			if args == nil {
				args = map[string]interface{}{}
			}
			req.Params.Arguments = map[string]interface{}{
				"name": tc.server + ":" + tc.tool, "args": args,
				"intent": map[string]interface{}{"operation_type": "read"},
			}
			_, err := mcpClient.CallTool(ctx, req)
			require.NoError(t, err)

			rec := awaitToolCallActivity(t, rt, tc.server, tc.tool)
			assert.Equal(t, tc.wantClass, rec.ErrorClass, "persisted error_class")
			assert.Equal(t, tc.wantDomain, rec.FaultDomain, "persisted fault_domain")
			assert.Equal(t, tc.wantStatus, rec.UpstreamHTTPStatus, "persisted upstream_http_status")
			if tc.wantClass == "" {
				assert.Equal(t, storage.ActivityStatusSuccess, rec.Status)
			} else {
				// FR-048: every failure keeps status "error", isError included.
				assert.Equal(t, storage.ActivityStatusError, rec.Status)
			}

			// REST: GET /api/v1/activity carries the same fields.
			row := restActivityRow(t, env, tc.server, tc.tool, "")
			if tc.wantClass == "" {
				assert.NotContains(t, row, "error_class")
				assert.NotContains(t, row, "fault_domain")
				assert.NotContains(t, row, "upstream_http_status")
				return
			}
			assert.Equal(t, tc.wantClass, row["error_class"])
			assert.Equal(t, tc.wantDomain, row["fault_domain"])
			if tc.wantStatus == 0 {
				assert.NotContains(t, row, "upstream_http_status")
			} else {
				assert.EqualValues(t, tc.wantStatus, row["upstream_http_status"])
			}

			// FR-046: the class filter selects the row; a different class does not.
			assert.NotNil(t, restActivityRow(t, env, tc.server, tc.tool, "&error_class="+tc.wantClass))
			assert.Nil(t, restActivityRowMaybe(t, env, tc.server, tc.tool, "&error_class=cancelled"))
			assert.NotNil(t, restActivityRow(t, env, tc.server, tc.tool, "&fault_domain="+tc.wantDomain))
		})
	}
}

func restActivityRowMaybe(t *testing.T, env *TestEnvironment, server, tool, extra string) map[string]interface{} {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/activity?type=tool_call&server=%s&tool=%s%s", env.baseURL(), server, tool, extra)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("X-Api-Key", "test-api-key-e2e")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var out struct {
		Data struct {
			Activities []map[string]interface{} `json:"activities"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	if len(out.Data.Activities) == 0 {
		return nil
	}
	return out.Data.Activities[0]
}

func restActivityRow(t *testing.T, env *TestEnvironment, server, tool, extra string) map[string]interface{} {
	t.Helper()
	row := restActivityRowMaybe(t, env, server, tool, extra)
	require.NotNil(t, row, "no REST activity row for %s:%s %s", server, tool, extra)
	return row
}

// FR-044: every tool-call write path stamps the taxonomy from the same
// classifier: direct surface (/mcp/all), code_execution sub-calls and the REST
// /api/v1/tools/call route, not only call_tool_*.
func TestCallErrorTaxonomy_EveryWritePath(t *testing.T) {
	upstream := newTaxonomyUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("p_direct", upstream.URL),
		fwdServerCfg("p_code", upstream.URL),
		fwdServerCfg("p_rest", upstream.URL),
	}, nil)
	rt := env.proxyServer.runtime

	t.Run("direct", func(t *testing.T) {
		c := env.fwdClient("/mcp/all", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req := mcp.CallToolRequest{}
		req.Params.Name = "p_direct__http502"
		req.Params.Arguments = map[string]interface{}{}
		_, err := c.CallTool(ctx, req)
		require.NoError(t, err)
		rec := awaitToolCallActivity(t, rt, "p_direct", "http502")
		assert.Equal(t, "http", rec.ErrorClass)
		assert.Equal(t, "upstream", rec.FaultDomain)
		assert.Equal(t, 502, rec.UpstreamHTTPStatus)
	})

	t.Run("code_execution sub-call", func(t *testing.T) {
		c := env.fwdClient("/mcp/code", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req := mcp.CallToolRequest{}
		req.Params.Name = "code_execution"
		req.Params.Arguments = map[string]interface{}{
			"code":  `var r = call_tool("p_code", "http502", {}); ({ ok: r.ok })`,
			"input": map[string]interface{}{},
		}
		_, err := c.CallTool(ctx, req)
		require.NoError(t, err)
		rec := awaitToolCallActivity(t, rt, "p_code", "http502")
		assert.Equal(t, "http", rec.ErrorClass)
		assert.Equal(t, "upstream", rec.FaultDomain)
		assert.Equal(t, 502, rec.UpstreamHTTPStatus)
	})

	t.Run("REST tools/call", func(t *testing.T) {
		body := strings.NewReader(`{"tool_name":"call_tool_read","arguments":{"name":"p_rest:http502","args":{},"intent":{"operation_type":"read"}}}`)
		req, err := http.NewRequest(http.MethodPost, env.baseURL()+"/api/v1/tools/call", body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Api-Key", "test-api-key-e2e")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		rec := awaitToolCallActivity(t, rt, "p_rest", "http502")
		assert.Equal(t, "http", rec.ErrorClass)
		assert.Equal(t, "upstream", rec.FaultDomain)
		assert.Equal(t, 502, rec.UpstreamHTTPStatus)
	})
}
