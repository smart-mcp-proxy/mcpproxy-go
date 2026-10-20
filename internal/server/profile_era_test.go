package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 058 US3 (FR-011): under the 2026-07-28 revision there is no session, so
// nothing a request does may persist per-connection state. The profile
// resolver's session tier (the legacy set_profile selection) is the one such
// state mcpproxy holds. An empty session id is NOT a sufficient guard: the
// stdio transport reports the constant "stdio" in both eras, so a modern stdio
// request would otherwise persist a selection like a legacy one (research D1,
// graft 1). The gate must be the era itself.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

// profileEraProxy is a bare proxy with one profile, for resolver-level cells.
func profileEraProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	return statelessListProxy(t)
}

// modernRequestCtx stamps ctx as a 2026-07-28 request, exactly as mcp-go's
// dispatch loop does before any hook or handler runs (request_handler.go).
func modernRequestCtx(ctx context.Context) context.Context {
	return mcpserver.WithRequestProtocolInfo(ctx, &mcpserver.RequestProtocolInfo{
		Modern:          true,
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      &mcp.Implementation{Name: "era-probe", Version: "1.0.0"},
	})
}

// The resolver's session tier applies to a legacy request on a session and is
// skipped — not cleared — for a modern request on the SAME non-empty session
// id. Both resolvers (v2 precedence and the Spec 108 v3 resolution) must agree.
func TestProfileEra_SessionTierSkippedOnModernRequests(t *testing.T) {
	proxy := profileEraProxy(t)
	proxy.sessionStore.SetActiveProfile("stdio", "research")

	legacy := sessionCtx(adminCtx(), "stdio")
	modern := modernRequestCtx(legacy)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	name, scope := proxy.resolveActiveProfile(legacy)
	require.Equal(t, "research", name, "a legacy request on the session honours its selection")
	require.NotNil(t, scope)

	name, scope = proxy.resolveActiveProfile(modern)
	assert.Equal(t, "", name, "a modern request must not inherit connection state, even on a non-empty session id")
	assert.Nil(t, scope)

	res := proxy.ResolveProfileV3(modern, idx)
	assert.Equal(t, "", res.Name)
	assert.Equal(t, string(profile.SourceNone), res.Source)
	assert.Nil(t, res.Scope)

	assert.Equal(t, "research", proxy.sessionStore.GetActiveProfile("stdio"),
		"skipping the tier is a read-side gate; a modern request neither applies nor clears a legacy selection")
}

// ─── stdio surface ──────────────────────────────────────────────────────────

// stdioProbe drives a proxy's default server over an in-process stdio
// transport, with the production stdio context (admin identity, stdio source).
type stdioProbe struct {
	t      *testing.T
	stdin  *io.PipeWriter
	stdout *bufio.Reader
	nextID int
}

func newStdioProbe(t *testing.T, srv *mcpserver.MCPServer) *stdioProbe {
	t.Helper()
	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetContextFunc(stdioAuthContext)

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = stdio.Listen(ctx, stdinR, stdoutW)
	}()
	t.Cleanup(func() {
		cancel()
		_ = stdinW.Close()
		_ = stdoutW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("stdio server did not stop within 5s")
		}
	})
	return &stdioProbe{t: t, stdin: stdinW, stdout: bufio.NewReader(stdoutR), nextID: 1}
}

// request writes one JSON-RPC request line and returns the response with the
// matching id, skipping any notifications the server emits in between.
func (p *stdioProbe) request(method string, params map[string]any) map[string]any {
	p.t.Helper()
	id := p.nextID
	p.nextID++
	if params == nil {
		params = map[string]any{}
	}
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	require.NoError(p.t, err)
	_, err = p.stdin.Write(append(line, '\n'))
	require.NoError(p.t, err)

	type read struct {
		msg map[string]any
		err error
	}
	for {
		ch := make(chan read, 1)
		go func() {
			raw, err := p.stdout.ReadBytes('\n')
			if err != nil {
				ch <- read{err: err}
				return
			}
			var msg map[string]any
			ch <- read{msg: msg, err: json.Unmarshal(raw, &msg)}
		}()
		select {
		case r := <-ch:
			require.NoError(p.t, r.err)
			if got, ok := r.msg["id"].(float64); ok && int(got) == id {
				return r.msg
			}
		case <-time.After(10 * time.Second):
			p.t.Fatalf("no response to %s (id %d) within 10s", method, id)
		}
	}
}

// modernMeta is the _meta a 2026-07-28 request carries in place of initialize.
func modernMeta() map[string]any {
	return map[string]any{
		mcp.MetaKeyProtocolVersion:    mcp.LATEST_PROTOCOL_VERSION,
		mcp.MetaKeyClientCapabilities: map[string]any{},
		mcp.MetaKeyClientInfo:         map[string]any{"name": "era-probe", "version": "1.0.0"},
	}
}

func profileEraRuntimeProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy, _ := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{
			{Name: "research-srv", Enabled: false},
			{Name: "deploy-srv", Enabled: false},
		}
		cfg.Profiles = []config.ProfileConfig{{Name: "research", Servers: []string{"research-srv"}}}
	})
	return proxy
}

// A legacy stdio session can select a profile (the control that proves the
// probe drives the real handler); a modern stdio request, whose session id is
// the same constant "stdio", cannot — the call is refused and nothing is
// stored.
func TestProfileEra_ModernStdioRequestCannotPersistSelection(t *testing.T) {
	t.Run("legacy stdio session persists the selection", func(t *testing.T) {
		proxy := profileEraRuntimeProxy(t)
		probe := newStdioProbe(t, proxy.GetMCPServer())

		init := probe.request(string(mcp.MethodInitialize), map[string]any{
			"protocolVersion": mcp.LATEST_LEGACY_PROTOCOL_VERSION,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "era-probe", "version": "1.0.0"},
		})
		require.Nil(t, init["error"], "legacy initialize must succeed: %v", init)

		resp := probe.request(string(mcp.MethodToolsCall), map[string]any{
			"name": "set_profile", "arguments": map[string]any{"profile": "research"},
		})
		require.Nil(t, resp["error"], "legacy set_profile must not be a JSON-RPC error: %v", resp)
		result := resp["result"].(map[string]any)
		require.NotEqual(t, true, result["isError"], "legacy set_profile must succeed: %v", result)
		assert.Equal(t, "research", proxy.sessionStore.GetActiveProfile("stdio"),
			"the legacy stdio session keeps its selection")
	})

	t.Run("modern stdio request is refused and persists nothing", func(t *testing.T) {
		proxy := profileEraRuntimeProxy(t)
		probe := newStdioProbe(t, proxy.GetMCPServer())

		resp := probe.request(string(mcp.MethodToolsCall), map[string]any{
			"name": "set_profile", "arguments": map[string]any{"profile": "research"}, "_meta": modernMeta(),
		})
		if resp["error"] == nil {
			result, ok := resp["result"].(map[string]any)
			require.True(t, ok, "response must carry a result or an error: %v", resp)
			assert.Equal(t, true, result["isError"], "a modern request must not be able to select a session profile: %v", result)
		}
		assert.Equal(t, "", proxy.sessionStore.GetActiveProfile("stdio"),
			"FR-011: a modern request must not persist per-connection state, despite stdio's non-empty session id")
	})
}

// ─── set_profile on a modern request (T026) ─────────────────────────────────

func setProfileRequest(slug string) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "set_profile"
	req.Params.Arguments = map[string]any{"profile": slug}
	return req
}

// On a modern request set_profile validates the slug FIRST — an unknown slug
// gets the same refusal in both eras — and then, instead of binding a
// selection it has no session for, answers machine-actionable guidance that
// names the per-request form `/mcp/p/<slug>`. Nothing is stored.
func TestProfileEra_SetProfileModernRequestGuidance(t *testing.T) {
	proxy := profileEraProxy(t)
	// The stdio session id is the hard case: non-empty in both eras.
	modern := modernRequestCtx(sessionCtx(adminCtx(), "stdio"))

	t.Run("unknown slug is refused as unknown, not as sessionless", func(t *testing.T) {
		result, err := proxy.handleSetProfile(modern, setProfileRequest("nonexistent"))
		require.NoError(t, err)
		require.True(t, result.IsError)
		text := extractText(result)
		assert.Contains(t, text, "unknown profile 'nonexistent'", "slug validation runs before the session guard: %s", text)
		assert.NotContains(t, text, "/mcp/p/", "an unknown slug must not be handed a URL it cannot use: %s", text)
	})

	t.Run("known slug is refused with the /mcp/p/<slug> form", func(t *testing.T) {
		result, err := proxy.handleSetProfile(modern, setProfileRequest("research"))
		require.NoError(t, err)
		require.True(t, result.IsError, "a modern request cannot bind a session selection")
		text := extractText(result)
		assert.Contains(t, text, "/mcp/p/research", "guidance must name the exact per-request endpoint: %s", text)
		assert.Contains(t, text, mcp.LATEST_PROTOCOL_VERSION, "guidance must say which protocol revision has no session: %s", text)
		assert.Equal(t, "", proxy.sessionStore.GetActiveProfile("stdio"), "nothing may be stored for a modern request")
	})

	t.Run("clearing is refused with the generic form", func(t *testing.T) {
		result, err := proxy.handleSetProfile(modern, setProfileRequest(""))
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Contains(t, extractText(result), "/mcp/p/<slug>")
	})

	t.Run("legacy request on the same session still selects", func(t *testing.T) {
		result, err := proxy.handleSetProfile(sessionCtx(adminCtx(), "stdio"), setProfileRequest("research"))
		require.NoError(t, err)
		require.False(t, result.IsError, "%s", extractText(result))
		assert.Equal(t, "research", proxy.sessionStore.GetActiveProfile("stdio"))
	})
}
