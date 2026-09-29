package server

// Spec 112 Phase 4 integration tests (T025-T032): a real proxy (all client
// facing mounts, storage, upstream manager) in front of an httptest
// streamable-HTTP upstream that records every request it receives.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	uptransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// ---------------------------------------------------------------------------
// Recording upstream
// ---------------------------------------------------------------------------

type fwdSeen struct {
	Verb   string // HTTP method
	RPC    string // JSON-RPC method ("" for non-POST)
	Tool   string // tools/call params.name
	Header http.Header
}

// fwdUpstream is a streamable-HTTP MCP server behind a recording wrapper. The
// wrapper also plays the two hostile upstreams of User Story 6: it echoes the
// X-Tenant-Id request header back as a response header, and answers the "boom"
// tool with a 500 whose body dumps every request header.
type fwdUpstream struct {
	URL  string
	mu   sync.Mutex
	seen []fwdSeen
}

func newFwdUpstream(t *testing.T) *fwdUpstream {
	t.Helper()
	srv := mcpserver.NewMCPServer("fwd-upstream", "1.0.0", mcpserver.WithToolCapabilities(true))
	noArgs := mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}}
	srv.AddTool(mcp.Tool{Name: "echo_headers", Description: "Echoes the request headers", InputSchema: noArgs},
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			b, _ := json.Marshal(req.Header)
			return mcp.NewToolResultText(string(b)), nil
		})
	srv.AddTool(mcp.Tool{Name: "whoami", Description: "Echoes the tenant header in three forms", InputSchema: noArgs},
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			v := req.Header.Get("X-Tenant-Id")
			return mcp.NewToolResultText(fmt.Sprintf("plain=%s json=%q name-anchored=X-Tenant-Id: %s", v, v, v)), nil
		})
	srv.AddTool(mcp.Tool{Name: "whoami_b64", Description: "Echoes the tenant header base64-encoded", InputSchema: noArgs},
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("b64=" + base64.StdEncoding.EncodeToString([]byte(req.Header.Get("X-Tenant-Id")))), nil
		})
	srv.AddTool(mcp.Tool{Name: "leak", Description: "Returns a secret alongside the tenant header", InputSchema: noArgs},
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("key=AKIA1234567890ABCDEF tenant=" + req.Header.Get("X-Tenant-Id")), nil
		})
	srv.AddTool(mcp.Tool{Name: "boom", Description: "Always fails (handled by the wrapper)", InputSchema: noArgs},
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("unreachable"), nil
		})

	u := &fwdUpstream{}
	inner := mcpserver.NewStreamableHTTPServer(srv)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		u.mu.Lock()
		u.seen = append(u.seen, fwdSeen{Verb: r.Method, RPC: msg.Method, Tool: msg.Params.Name, Header: r.Header.Clone()})
		u.mu.Unlock()

		if v := r.Header.Get("X-Tenant-Id"); v != "" {
			w.Header().Set("X-Tenant-Id", v) // response-header echo
		}
		if msg.Method == "tools/call" && msg.Params.Name == "boom" {
			w.WriteHeader(http.StatusInternalServerError)
			dump, _ := json.Marshal(r.Header)
			_, _ = fmt.Fprintf(w, "upstream exploded; request headers: %v ; json: %s ; X-Tenant-Id: %s", r.Header, dump, r.Header.Get("X-Tenant-Id"))
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	u.URL = hs.URL
	return u
}

func (u *fwdUpstream) all() []fwdSeen {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]fwdSeen(nil), u.seen...)
}

func (u *fwdUpstream) mark() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.seen)
}

func (u *fwdUpstream) since(i int) []fwdSeen {
	all := u.all()
	if i > len(all) {
		i = len(all)
	}
	return all[i:]
}

func fwdRPCCalls(seen []fwdSeen, rpc string) []fwdSeen {
	var out []fwdSeen
	for _, s := range seen {
		if s.RPC == rpc {
			out = append(out, s)
		}
	}
	return out
}

func fwdToolCalls(seen []fwdSeen, tool string) []fwdSeen {
	var out []fwdSeen
	for _, s := range seen {
		if s.RPC == "tools/call" && s.Tool == tool {
			out = append(out, s)
		}
	}
	return out
}

// hasHeaderValue reports whether any header of any request carries value.
func fwdHasValue(seen []fwdSeen, value string) bool {
	for _, s := range seen {
		for _, vs := range s.Header {
			for _, v := range vs {
				if strings.Contains(v, value) {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Proxy environment
// ---------------------------------------------------------------------------

func fwdServerCfg(name, url string, mutate ...func(*config.ServerConfig)) *config.ServerConfig {
	sc := &config.ServerConfig{Name: name, URL: url, Protocol: "streamable-http", Enabled: true, Created: time.Now()}
	for _, m := range mutate {
		m(sc)
	}
	return sc
}

func withForward(names ...string) func(*config.ServerConfig) {
	return func(sc *config.ServerConfig) { sc.ForwardHeaders = names }
}

// startFwdEnv boots a full proxy with the given upstream servers pre-configured
// and waits for every enabled one to connect and be indexed.
func startFwdEnv(t *testing.T, servers []*config.ServerConfig, mutate func(*config.Config, string)) *TestEnvironment {
	t.Helper()
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, tempDir string) {
			cfg.Servers = servers
			cfg.EnableCodeExecution = true
			if mutate != nil {
				mutate(cfg, tempDir)
			}
		},
	})
	t.Cleanup(env.Cleanup)
	env.waitFwdConnected(servers)
	return env
}

func (env *TestEnvironment) waitFwdConnected(servers []*config.ServerConfig) {
	t := env.t
	t.Helper()
	rt := env.proxyServer.runtime
	deadline := time.Now().Add(20 * time.Second)
	for _, sc := range servers {
		if !sc.Enabled {
			continue
		}
		for {
			if c, ok := rt.UpstreamManager().GetClient(sc.Name); ok && c.IsConnected() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("upstream %q did not connect", sc.Name)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	require.NoError(t, rt.DiscoverAndIndexTools(context.Background()))
}

func (env *TestEnvironment) baseURL() string { return strings.TrimSuffix(env.proxyAddr, "/mcp") }

// fwdClient connects an MCP client to path on the proxy, sending hdr on every
// request it makes.
func (env *TestEnvironment) fwdClient(path string, hdr map[string]string) *client.Client {
	t := env.t
	t.Helper()
	tr, err := transport.NewStreamableHTTP(env.baseURL()+path, transport.WithHTTPHeaders(hdr))
	require.NoError(t, err)
	c := client.NewClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, c.Start(ctx))
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "fwd-test", Version: "1"}
	_, err = c.Initialize(ctx, req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func fwdText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func fwdCallReadRaw(c *client.Client, server, tool string) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = "call_tool_read"
	req.Params.Arguments = map[string]interface{}{
		"name": server + ":" + tool, "args": map[string]interface{}{},
		"intent": map[string]interface{}{"operation_type": "read"},
	}
	return c.CallTool(ctx, req)
}

func fwdCallRead(t *testing.T, c *client.Client, server, tool string) string {
	t.Helper()
	res, err := fwdCallReadRaw(c, server, tool)
	require.NoError(t, err)
	require.False(t, res.IsError, "%s:%s -> %s", server, tool, fwdText(res))
	return fwdText(res)
}

func fwdCallDirect(t *testing.T, c *client.Client, server, tool string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = server + "__" + tool
	req.Params.Arguments = map[string]interface{}{}
	res, err := c.CallTool(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "%s__%s -> %s", server, tool, fwdText(res))
	return fwdText(res)
}

func fwdCallCode(t *testing.T, c *client.Client, server, tool string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = "code_execution"
	req.Params.Arguments = map[string]interface{}{
		"code":  fmt.Sprintf(`var r = call_tool(%q, %q, {}); ({ ok: r.ok })`, server, tool),
		"input": map[string]interface{}{},
	}
	res, err := c.CallTool(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "code_execution -> %s", fwdText(res))
	return fwdText(res)
}

// ---------------------------------------------------------------------------
// T025 - allowed, missing, non-allowlisted and other-server, on every surface
// ---------------------------------------------------------------------------

func TestForwardHeaders_AllowedHeaderReachesUpstreamOnEverySurface(t *testing.T) {
	gw := newFwdUpstream(t)
	other := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("gw", gw.URL, withForward("X-User-Id", "X-Missing")),
		fwdServerCfg("other", other.URL),
	}, nil)

	surfaces := []struct {
		name string
		path string
		call func(*testing.T, *client.Client)
	}{
		{"call_tool_read", "/mcp", func(t *testing.T, c *client.Client) { fwdCallRead(t, c, "gw", "echo_headers") }},
		{"direct", "/mcp/all", func(t *testing.T, c *client.Client) { fwdCallDirect(t, c, "gw", "echo_headers") }},
		{"code_execution", "/mcp/code", func(t *testing.T, c *client.Client) { fwdCallCode(t, c, "gw", "echo_headers") }},
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			value := "alice-" + s.name
			before := gw.mark()
			otherBefore := other.mark()
			c := env.fwdClient(s.path, map[string]string{"X-User-Id": value, "X-Other": "1"})
			s.call(t, c)

			calls := fwdToolCalls(gw.since(before), "echo_headers")
			require.Len(t, calls, 1)
			assert.Equal(t, value, calls[0].Header.Get("X-User-Id"), "allowlisted header must be forwarded")
			assert.NotContains(t, calls[0].Header, "X-Other", "a header outside the allowlist is never sent")
			_, present := calls[0].Header["X-Missing"]
			assert.False(t, present, "an allowlisted header the client did not send is omitted, not empty")
			assert.False(t, fwdHasValue(other.since(otherBefore), value), "another server gets nothing")
			// Nothing but the tools/call may carry the header.
			for _, r := range gw.since(before) {
				if r.RPC != "tools/call" {
					assert.Empty(t, r.Header.Get("X-User-Id"), "%s must not carry the forwarded header", r.RPC)
				}
			}
		})
	}

	t.Run("other server receives nothing", func(t *testing.T) {
		before := other.mark()
		c := env.fwdClient("/mcp", map[string]string{"X-User-Id": "bob", "X-Other": "1"})
		fwdCallRead(t, c, "other", "echo_headers")
		calls := fwdToolCalls(other.since(before), "echo_headers")
		require.Len(t, calls, 1)
		assert.Empty(t, calls[0].Header.Get("X-User-Id"))
		assert.NotContains(t, calls[0].Header, "X-Other")
		assert.False(t, fwdHasValue(other.since(before), "bob"))
	})

	t.Run("client omits the header", func(t *testing.T) {
		before := gw.mark()
		c := env.fwdClient("/mcp", nil)
		fwdCallRead(t, c, "gw", "echo_headers")
		calls := fwdToolCalls(gw.since(before), "echo_headers")
		require.Len(t, calls, 1)
		_, present := calls[0].Header["X-User-Id"]
		assert.False(t, present)
	})
}

// ---------------------------------------------------------------------------
// T026 - 50 concurrent clients, own value on every call, -race
// ---------------------------------------------------------------------------

func TestForwardHeaders_FiftyConcurrentClientsKeepTheirOwnValue(t *testing.T) {
	gw := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("gw", gw.URL, withForward("X-User-Id")),
	}, nil)
	startMark := gw.mark()

	const clients, callsEach = 50, 3
	var wg sync.WaitGroup
	errs := make(chan string, clients*callsEach*2)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := "user-" + strconv.Itoa(i) + "-" + strings.Repeat("x", i%7)
			tr, err := transport.NewStreamableHTTP(env.proxyAddr, transport.WithHTTPHeaders(map[string]string{"X-User-Id": value}))
			if err != nil {
				errs <- err.Error()
				return
			}
			c := client.NewClient(tr)
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := c.Start(ctx); err != nil {
				errs <- "start: " + err.Error()
				return
			}
			init := mcp.InitializeRequest{}
			init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
			init.Params.ClientInfo = mcp.Implementation{Name: "c", Version: "1"}
			if _, err := c.Initialize(ctx, init); err != nil {
				errs <- "init: " + err.Error()
				return
			}
			for j := 0; j < callsEach; j++ {
				res, err := fwdCallReadRaw(c, "gw", "echo_headers")
				if err != nil || res.IsError {
					errs <- fmt.Sprintf("call: %v %s", err, fwdText(res))
					return
				}
				var hdr map[string][]string
				if err := json.Unmarshal([]byte(fwdText(res)), &hdr); err != nil {
					errs <- "decode: " + err.Error()
					return
				}
				if got := strings.Join(hdr["X-User-Id"], ","); got != value {
					errs <- fmt.Sprintf("client %d saw %q want %q", i, got, value)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	seen := gw.since(startMark)
	perValue := map[string]int{}
	for _, s := range seen {
		v := s.Header.Get("X-User-Id")
		if s.RPC == "tools/call" && s.Tool == "echo_headers" {
			require.NotEmpty(t, v, "every upstream tools/call must carry a caller's value")
			perValue[v]++
			continue
		}
		assert.Empty(t, v, "%s %s must not carry a forwarded header", s.Verb, s.RPC)
	}
	assert.Len(t, perValue, clients, "one distinct value per client")
	for v, n := range perValue {
		assert.Equal(t, callsEach, n, "value %q", v)
	}
}

// ---------------------------------------------------------------------------
// T027 - denied names are never forwarded (plain and static-header upstreams;
// the OAuth branch is covered in internal/transport/headerfwd_denied_test.go)
// ---------------------------------------------------------------------------

func TestForwardHeaders_DeniedNamesNeverForwarded(t *testing.T) {
	// Names an HTTP client can actually put on a request; the rest of FR-004
	// (Host, Connection, Content-Type, Mcp-Session-Id, ...) is exercised by the
	// headerfwd unit tests and the transport tests.
	denied := []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie", "Forwarded", "X-Real-Ip",
		"X-Forwarded-For", "X-Forwarded-Host", "Traceparent", "Tracestate", "Baggage", "X-Request-Id",
		"User-Agent", "Referer", "If-Match", "Sec-Fetch-Site", "X-Mcpproxy-Debug", "Mcp-Custom",
	}
	plain := newFwdUpstream(t)
	static := newFwdUpstream(t)
	// The allowlists list every denied name in three casings (the config is
	// hand-built, as a hand-edited file would be) next to one legitimate name.
	var names []string
	for _, n := range denied {
		names = append(names, n, strings.ToLower(n), strings.ToUpper(n))
	}
	names = append(names, "X-Tenant-Id")
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("plain", plain.URL, withForward(names...)),
		fwdServerCfg("static", static.URL, withForward(names...), func(sc *config.ServerConfig) {
			sc.Headers = map[string]string{"X-Static-Token": "operator-secret"}
		}),
	}, nil)

	hdr := map[string]string{"X-Tenant-Id": "tenant-ok"}
	values := map[string]string{}
	for i, n := range denied {
		v := fmt.Sprintf("DENIED-%d-%s", i, strings.ToLower(n))
		hdr[n] = v
		values[n] = v
	}
	c := env.fwdClient("/mcp", hdr)
	for _, srv := range []struct {
		name string
		up   *fwdUpstream
	}{{"plain", plain}, {"static", static}} {
		before := srv.up.mark()
		fwdCallRead(t, c, srv.name, "echo_headers")
		calls := fwdToolCalls(srv.up.since(before), "echo_headers")
		require.Len(t, calls, 1, srv.name)
		assert.Equal(t, "tenant-ok", calls[0].Header.Get("X-Tenant-Id"), "%s: the legitimate name still flows", srv.name)
		for n, v := range values {
			assert.False(t, fwdHasValue(calls, v), "%s: denied header %s value leaked upstream", srv.name, n)
		}
	}
	// The proxy's own credentials to the static upstream are untouched.
	staticCalls := fwdToolCalls(static.all(), "echo_headers")
	require.NotEmpty(t, staticCalls)
	assert.Equal(t, "operator-secret", staticCalls[0].Header.Get("X-Static-Token"))
}

// ---------------------------------------------------------------------------
// T028 - static collision: static wins; configs without forward_headers are
// unchanged
// ---------------------------------------------------------------------------

func TestForwardHeaders_StaticHeaderWinsAndUnconfiguredServersUnchanged(t *testing.T) {
	collide := newFwdUpstream(t)
	control := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("collide", collide.URL, withForward("X-Tenant-Id", "X-User-Id"), func(sc *config.ServerConfig) {
			sc.Headers = map[string]string{"X-Tenant-Id": "operator-secret"}
		}),
		fwdServerCfg("control", control.URL, func(sc *config.ServerConfig) {
			sc.Headers = map[string]string{"X-Static-Token": "s3"}
		}),
	}, nil)

	c := env.fwdClient("/mcp", map[string]string{"X-Tenant-Id": "attacker", "X-User-Id": "alice", "X-Extra": "e"})
	fwdCallRead(t, c, "collide", "echo_headers")
	calls := fwdToolCalls(collide.all(), "echo_headers")
	require.Len(t, calls, 1)
	assert.Equal(t, "operator-secret", calls[0].Header.Get("X-Tenant-Id"), "the static value wins over the client's")
	assert.Equal(t, "alice", calls[0].Header.Get("X-User-Id"), "the non-colliding allowlisted name still flows")
	assert.False(t, fwdHasValue(collide.all(), "attacker"))

	fwdCallRead(t, c, "control", "echo_headers")
	ccalls := fwdToolCalls(control.all(), "echo_headers")
	require.Len(t, ccalls, 1)
	baseline := map[string]bool{
		"Accept": true, "Accept-Encoding": true, "Content-Length": true, "Content-Type": true,
		"Mcp-Protocol-Version": true, "Mcp-Session-Id": true, "User-Agent": true, "X-Static-Token": true,
	}
	for _, r := range control.all() {
		for name := range r.Header {
			assert.True(t, baseline[name], "control server got an unexpected header %q on %s", name, r.RPC)
		}
		assert.False(t, fwdHasValue([]fwdSeen{r}, "attacker") || fwdHasValue([]fwdSeen{r}, "alice"))
	}
	assert.Equal(t, "s3", ccalls[0].Header.Get("X-Static-Token"))
}

// ---------------------------------------------------------------------------
// T029 - reconnect_on_use and refresh never carry the caller's headers
// ---------------------------------------------------------------------------

func TestForwardHeaders_ReconnectAndRefreshCarryNothing(t *testing.T) {
	gw := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("gw", gw.URL, withForward("X-User-Id"), func(sc *config.ServerConfig) { sc.ReconnectOnUse = true }),
	}, nil)
	rt := env.proxyServer.runtime
	mc, ok := rt.UpstreamManager().GetClient("gw")
	require.True(t, ok)

	// reconnect_on_use is honoured by the direct surface (call_tool_* checks
	// liveness first and refuses; code_execution calls the managed client without
	// reconnecting). Drop the shared session so
	// the next call reconnects under client A's request context.
	for _, surface := range []struct {
		name, path string
		call       func(*testing.T, *client.Client)
	}{
		{"direct", "/mcp/all", func(t *testing.T, c *client.Client) { fwdCallDirect(t, c, "gw", "echo_headers") }},
	} {
		t.Run("reconnect_on_use via "+surface.name, func(t *testing.T) {
			require.NoError(t, mc.Disconnect())
			require.False(t, mc.IsConnected())

			before := gw.mark()
			value := "client-a-" + surface.name
			a := env.fwdClient(surface.path, map[string]string{"X-User-Id": value})
			surface.call(t, a)

			seen := gw.since(before)
			require.NotEmpty(t, fwdRPCCalls(seen, "initialize"), "the call must have reconnected the session")
			for _, r := range seen {
				if r.RPC == "tools/call" && r.Tool == "echo_headers" {
					assert.Equal(t, value, r.Header.Get("X-User-Id"), "the tools/call itself carries the caller's header")
					continue
				}
				assert.Empty(t, r.Header.Get("X-User-Id"), "%s %s must not carry the forwarded header", r.Verb, r.RPC)
			}
			assert.Len(t, fwdToolCalls(seen, "echo_headers"), 1)
		})
	}

	a := env.fwdClient("/mcp", map[string]string{"X-User-Id": "client-a"})
	// upstream_servers refresh runs ListTools under client A's request ctx.
	before := gw.mark()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = "upstream_servers"
	req.Params.Arguments = map[string]interface{}{"operation": "refresh", "name": "gw"}
	_, err := a.CallTool(ctx, req)
	require.NoError(t, err)
	// And background discovery.
	require.NoError(t, rt.DiscoverAndIndexToolsForServer(context.Background(), "gw"))
	for _, r := range gw.since(before) {
		assert.Empty(t, r.Header.Get("X-User-Id"), "%s %s (refresh/discovery) must carry nothing", r.Verb, r.RPC)
	}
}

// ---------------------------------------------------------------------------
// T030 - disabled via config and via env
// ---------------------------------------------------------------------------

func TestForwardHeaders_DisabledViaConfigNothingForwarded(t *testing.T) {
	gw := newFwdUpstream(t)
	off := false
	env := startFwdEnv(t, []*config.ServerConfig{fwdServerCfg("gw", gw.URL, withForward("X-User-Id"))},
		func(cfg *config.Config, _ string) { cfg.ForwardClientHeaders = &off })
	c := env.fwdClient("/mcp", map[string]string{"X-User-Id": "alice"})
	fwdCallRead(t, c, "gw", "echo_headers")
	require.Len(t, fwdToolCalls(gw.all(), "echo_headers"), 1)
	assert.False(t, fwdHasValue(gw.all(), "alice"))
}

func TestForwardHeaders_DisabledViaEnvNothingForwarded(t *testing.T) {
	t.Setenv(config.EnvForwardClientHeaders, "false")
	gw := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{fwdServerCfg("gw", gw.URL, withForward("X-User-Id"))}, nil)
	assert.False(t, env.proxyServer.runtime.Config().IsClientHeaderForwardingEnabled())
	c := env.fwdClient("/mcp", map[string]string{"X-User-Id": "alice"})
	fwdCallRead(t, c, "gw", "echo_headers")
	require.Len(t, fwdToolCalls(gw.all(), "echo_headers"), 1)
	assert.False(t, fwdHasValue(gw.all(), "alice"))
}

// ---------------------------------------------------------------------------
// T031 / T031a - echo redaction across every sink
// ---------------------------------------------------------------------------

const echoSentinel = "SENTINEL-erin-7c1d9f"

// captureStdout redirects os.Stdout (where the trace transport prints request
// and response headers) until the returned function is called.
func fwdCaptureStdout(t *testing.T) func() string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, r); close(done) }()
	var once sync.Once
	var out string
	stop := func() string {
		once.Do(func() {
			os.Stdout = orig
			_ = w.Close()
			<-done
			_ = r.Close()
			out = buf.String()
		})
		return out
	}
	t.Cleanup(func() { stop() })
	return stop
}

// grepTree returns every file under root whose bytes contain needle.
func fwdGrepTree(t *testing.T, root, needle string) []string {
	t.Helper()
	var hits []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > 256<<20 {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && bytes.Contains(b, []byte(needle)) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

func TestForwardHeaders_EchoedValuesReachNoSink(t *testing.T) {
	prevTrace := uptransport.GlobalTraceEnabled()
	uptransport.SetGlobalTraceEnabled(true)
	t.Cleanup(func() { uptransport.SetGlobalTraceEnabled(prevTrace) })
	stopStdout := fwdCaptureStdout(t)

	obsCore, observed := observer.New(zapcore.DebugLevel)
	up := newFwdUpstream(t)
	servers := []*config.ServerConfig{fwdServerCfg("echoer", up.URL, withForward("X-Tenant-Id"))}
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, tempDir string) {
			cfg.Servers = servers
			cfg.Logging = &config.LogConfig{
				Level: "debug", EnableFile: true, EnableConsole: false, Filename: "main.log",
				LogDir: filepath.Join(tempDir, "logs"), MaxSize: 10, MaxBackups: 1, MaxAge: 1,
			}
		},
		Logger: func(cfg *config.Config) (*zap.Logger, error) {
			l, err := logs.SetupLogger(cfg.Logging)
			if err != nil {
				return nil, err
			}
			logger := zap.New(zapcore.NewTee(l.Core(), obsCore))
			// The trace transport logs through zap.L(), as in production.
			undo := zap.ReplaceGlobals(logger)
			t.Cleanup(undo)
			return logger, nil
		},
	})
	t.Cleanup(env.Cleanup)
	env.waitFwdConnected(servers)

	c := env.fwdClient("/mcp", map[string]string{"X-Tenant-Id": echoSentinel})

	// T031a: successful echo (exact, JSON-escaped, name-anchored) + a response
	// header echo. The client must still get its own value back, unmodified.
	okText := fwdCallRead(t, c, "echoer", "whoami")
	assert.Contains(t, okText, echoSentinel, "the calling client receives the unmodified result")
	b64 := base64.StdEncoding.EncodeToString([]byte(echoSentinel))
	b64Text := fwdCallRead(t, c, "echoer", "whoami_b64")
	assert.Contains(t, b64Text, b64)

	// T031: a 500 whose body dumps every request header.
	res, err := fwdCallReadRaw(c, "echoer", "boom")
	var clientErr string
	if err != nil {
		clientErr = err.Error()
	} else {
		clientErr = fwdText(res)
		assert.True(t, res.IsError)
	}
	assert.NotEmpty(t, clientErr)
	assert.NotContains(t, clientErr, echoSentinel, "the error returned to the client")

	rt := env.proxyServer.runtime
	okRec := awaitToolCallActivity(t, rt, "echoer", "whoami")
	assert.NotContains(t, okRec.Response, echoSentinel, "activity Response")
	assert.Contains(t, okRec.Response, "[forwarded:X-Tenant-Id]")
	boomRec := awaitToolCallActivity(t, rt, "echoer", "boom")
	assert.NotContains(t, boomRec.ErrorMessage, echoSentinel, "activity ErrorMessage")
	assert.NotContains(t, boomRec.Response, echoSentinel)
	// Documented boundary (Known Limitation 7): a transformed echo is not caught.
	b64Rec := awaitToolCallActivity(t, rt, "echoer", "whoami_b64")
	assert.Contains(t, b64Rec.Response, b64, "base64 echoes are outside the guarantee")

	if mc, ok := rt.UpstreamManager().GetClient("echoer"); ok {
		if lerr := mc.GetLastError(); lerr != nil {
			assert.NotContains(t, lerr.Error(), echoSentinel, "server health LastError")
		}
	}
	// Every activity record, serialised (covers args, response, error, metadata).
	recs, _, lerr := rt.ListActivities(storage.ActivityFilter{Limit: 500})
	require.NoError(t, lerr)
	for _, r := range recs {
		if r.ToolName == "whoami_b64" {
			continue
		}
		raw, _ := json.Marshal(r)
		assert.NotContains(t, string(raw), echoSentinel, "activity record %s", r.ID)
	}

	stdout := stopStdout()
	assert.Contains(t, stdout, "HTTP REQUEST", "trace transport must have been active")
	assert.NotContains(t, stdout, echoSentinel, "trace stdout")

	var logText strings.Builder
	for _, e := range observed.All() {
		logText.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&logText, " %s=%v", k, v)
		}
		logText.WriteByte('\n')
	}
	assert.NotContains(t, logText.String(), echoSentinel, "zap log entries")

	// main.log, the per-server log, BBolt (activity, tool-call records) and every
	// other file the process wrote.
	assert.NotEmpty(t, fwdGrepTree(t, env.tempDir, "RESPONSE BODY"), "the trace body log must have run, or the scrub assertion below is vacuous")
	hits := fwdGrepTree(t, env.tempDir, echoSentinel)
	sort.Strings(hits)
	assert.Empty(t, hits, "the sentinel must not be persisted anywhere under the data/log dirs")

	// The upstream really did receive it (the test proves the leak path exists).
	assert.True(t, fwdHasValue(fwdToolCalls(up.all(), "whoami"), echoSentinel))
}

// ---------------------------------------------------------------------------
// T031b - mount coverage, enumerated from the router source
// ---------------------------------------------------------------------------

// mcpMountPatterns lists every mux pattern in server.go that serves an MCP
// endpoint. A new mount must show up here (and get a strategy below) or the
// test fails, which is the point (SC-007).
func mcpMountPatterns(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "server.go", nil, 0)
	require.NoError(t, err)
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Handle" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		p, _ := strconv.Unquote(lit.Value)
		if p == "/mcp" || strings.HasPrefix(p, "/mcp/") || strings.HasPrefix(p, "/v1/tool") {
			seen[p] = true
		}
		return true
	})
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func TestForwardHeaders_EveryClientFacingMountForwards(t *testing.T) {
	gw := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("gw", gw.URL, withForward("X-User-Id")),
	}, func(cfg *config.Config, _ string) {
		cfg.Profiles = []config.ProfileConfig{{Name: "fwd", Servers: []string{"gw"}, Unannotated: "as_read"}}
	})

	patterns := mcpMountPatterns(t)
	require.NotEmpty(t, patterns)
	var mounts []string
	for _, p := range patterns {
		switch p {
		case "/mcp/", "/mcp/all/", "/mcp/code/", "/mcp/call/":
			continue // subtree twin of the exact pattern, same handler
		case "/mcp/p", "/mcp/p/":
			if p == "/mcp/p/" {
				mounts = append(mounts, "/mcp/p/fwd")
			}
			continue
		}
		mounts = append(mounts, p)
	}
	assert.ElementsMatch(t, []string{"/mcp", "/mcp/all", "/mcp/code", "/mcp/call", "/mcp/p/fwd", "/v1/tool_code", "/v1/tool-code"}, mounts,
		"a mount was added or removed: give it a strategy here so its forwarding is covered")

	for _, path := range mounts {
		t.Run(path, func(t *testing.T) {
			value := "mount-" + strings.NewReplacer("/", "_").Replace(path)
			c := env.fwdClient(path, map[string]string{"X-User-Id": value})
			before := gw.mark()
			switch path {
			case "/mcp/all":
				fwdCallDirect(t, c, "gw", "echo_headers")
			case "/mcp/code":
				fwdCallCode(t, c, "gw", "echo_headers")
			default:
				fwdCallRead(t, c, "gw", "echo_headers")
			}
			calls := fwdToolCalls(gw.since(before), "echo_headers")
			require.Len(t, calls, 1)
			assert.Equal(t, value, calls[0].Header.Get("X-User-Id"), "mount %s must forward the allowlisted header", path)
		})
	}
}

// ---------------------------------------------------------------------------
// T032 - REST /api/v1/tools/call forwards nothing
// ---------------------------------------------------------------------------

func TestForwardHeaders_RESTToolsCallForwardsNothing(t *testing.T) {
	gw := newFwdUpstream(t)
	env := startFwdEnv(t, []*config.ServerConfig{
		fwdServerCfg("gw", gw.URL, withForward("X-User-Id")),
	}, nil)

	before := gw.mark()
	body := strings.NewReader(`{"tool_name":"call_tool_read","arguments":{"name":"gw:echo_headers","args":{},"intent":{"operation_type":"read"}}}`)
	req, err := http.NewRequest(http.MethodPost, env.baseURL()+"/api/v1/tools/call", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "test-api-key-e2e")
	req.Header.Set("X-User-Id", "rest-caller")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	calls := fwdToolCalls(gw.since(before), "echo_headers")
	require.Len(t, calls, 1, "the REST call must have reached the upstream")
	assert.Empty(t, calls[0].Header.Get("X-User-Id"), "REST tools/call forwards nothing (FR-006)")
	assert.False(t, fwdHasValue(gw.since(before), "rest-caller"))
	assert.NotContains(t, calls[0].Header, "X-Api-Key")
}
