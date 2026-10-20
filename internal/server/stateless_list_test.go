package server

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 058 US3, FR-012: `tools/list`, `prompts/list` and `resources/list` must
// not vary per connection. Identity-scoped views are driven by request-carried
// identity (token, URL, _meta), never by connection state. The one piece of
// connection state mcpproxy holds is the legacy `set_profile` selection (tier
// 3 of the profile resolver), so these tests pin two things:
//
//   - the canonical list facade is byte-identical across two connections in
//     BOTH protocol eras (the modern era binds no session at all);
//   - the two list filters that used to consult the session tier (direct-mode
//     tools, aggregated prompts) no longer do, so a legacy session that ran
//     set_profile gets the same list as a fresh one. Execution-time gates keep
//     honouring the selection — the split is deliberate and asserted below.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

// ─── era-reachable proxy endpoint ───────────────────────────────────────────

// unpinnedProxyEndpoint mounts srv over an UNPINNED Streamable HTTP transport
// behind the same auth middleware production wraps every /mcp* endpoint with.
// The FR-028 pin is deliberately left off: until T041 lifts it on the listening
// endpoint, this is the only way forEachEra can reach the modern era against a
// real proxy, and the behaviour under test here is exactly what that lift will
// expose. Everything else — auth context, direct-request-kind box, the
// cancellation guard — is the production chain.
func unpinnedProxyEndpoint(t *testing.T, s *Server, srv *mcpserver.MCPServer) string {
	t.Helper()
	streamable := mcpserver.NewStreamableHTTPServer(srv,
		mcpserver.WithDisableLocalhostProtection(true),
		mcpserver.WithHTTPContextFunc(captureClientHeaders(s.liveConfig)),
	)
	httpServer := httptest.NewServer(s.mcpAuthMiddleware(streamable))
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

// rawEraClient speaks JSON-RPC over HTTP by hand, pinned to one era, so a test
// can compare the exact bytes two connections receive. The mcp-go client would
// decode and re-encode them, which hides ordering and _meta differences.
type rawEraClient struct {
	t         *testing.T
	url       string
	era       protocolEra
	sessionID string
	nextID    int
}

func newRawEraClient(t *testing.T, url string, era protocolEra) *rawEraClient {
	t.Helper()
	c := &rawEraClient{t: t, url: url, era: era, nextID: 1}
	if !c.modern() {
		resp, msg := c.send(string(mcp.MethodInitialize), map[string]any{
			"protocolVersion": era.version,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "raw-era-client", "version": "1.0.0"},
		}, true)
		require.Equal(t, http.StatusOK, resp.StatusCode, "legacy initialize must succeed: %s", msg)
		c.sessionID = resp.Header.Get(mcpserver.HeaderKeySessionID)
		require.NotEmpty(t, c.sessionID, "the legacy era binds a session id on initialize")
		c.send(string(mcp.MethodNotificationInitialized), nil, false)
	}
	return c
}

func (c *rawEraClient) modern() bool { return mcp.IsModernProtocol(c.era.version) }

// send posts one JSON-RPC message and returns the HTTP response plus the
// decoded JSON-RPC payload (SSE-framed responses are unwrapped).
func (c *rawEraClient) send(method string, params map[string]any, withID bool) (*http.Response, []byte) {
	c.t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if c.modern() {
		params["_meta"] = map[string]any{
			mcp.MetaKeyProtocolVersion:    c.era.version,
			mcp.MetaKeyClientCapabilities: map[string]any{},
			mcp.MetaKeyClientInfo:         map[string]any{"name": "raw-era-client", "version": "1.0.0"},
		}
	}
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if withID {
		msg["id"] = c.nextID
		c.nextID++
	}
	body, err := json.Marshal(msg)
	require.NoError(c.t, err)

	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.modern() {
		// SEP-2243: a modern POST mirrors its method (and the tool/prompt
		// name) into routing headers; mcp-go refuses a request that omits them.
		req.Header.Set(mcp.HeaderProtocolVersion, c.era.version)
		req.Header.Set(mcp.HeaderMethod, method)
		if name, ok := params["name"].(string); ok {
			req.Header.Set(mcp.HeaderName, name)
		}
	} else if c.sessionID != "" {
		req.Header.Set(mcpserver.HeaderKeySessionID, c.sessionID)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	return resp, unwrapJSONRPCBody(resp.Header.Get("Content-Type"), raw)
}

// unwrapJSONRPCBody returns the JSON-RPC message carried by an HTTP response:
// the body itself for application/json, or the last `data:` frame of an SSE
// response (mcp-go upgrades to SSE when a handler emits notifications).
func unwrapJSONRPCBody(contentType string, raw []byte) []byte {
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return raw
	}
	var last []byte
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			last = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return last
}

// result sends a request and returns the raw bytes of its `result` member,
// failing the test on a JSON-RPC error.
func (c *rawEraClient) result(method string, params map[string]any) json.RawMessage {
	c.t.Helper()
	resp, msg := c.send(method, params, true)
	require.Equal(c.t, http.StatusOK, resp.StatusCode, "%s must succeed: %s", method, msg)
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	require.NoError(c.t, json.Unmarshal(msg, &envelope), "response must be JSON-RPC: %s", msg)
	require.Nil(c.t, envelope.Error, "%s must not fail: %s", method, envelope.Error)
	require.NotNil(c.t, envelope.Result, "%s must carry a result: %s", method, msg)
	return envelope.Result
}

// callTool sends tools/call and returns the decoded result.
func (c *rawEraClient) callTool(name string, args map[string]any) mcp.CallToolResult {
	c.t.Helper()
	raw := c.result(string(mcp.MethodToolsCall), map[string]any{"name": name, "arguments": args})
	var result mcp.CallToolResult
	require.NoError(c.t, json.Unmarshal(raw, &result))
	return result
}

// requireEra fails unless the connection observably took the era it was
// pinned to: the legacy era binds a session id, the modern era never does and
// decorates every result with a resultType. Without this a server pin would
// turn the modern subtest into a second legacy run (research D7).
func (c *rawEraClient) requireEra(result json.RawMessage) {
	c.t.Helper()
	var decorated struct {
		ResultType string `json:"resultType"`
	}
	require.NoError(c.t, json.Unmarshal(result, &decorated))
	if c.modern() {
		require.Empty(c.t, c.sessionID, "the modern era must not bind a session id")
		require.Equal(c.t, "complete", decorated.ResultType,
			"a modern result carries resultType; its absence means the %s request was served on the legacy path", c.era.version)
		return
	}
	require.NotEmpty(c.t, c.sessionID, "the legacy era must bind a session id")
	require.Empty(c.t, decorated.ResultType, "a legacy result carries no resultType")
}

// ─── FR-012 across connections, both eras (T020) ────────────────────────────

// Two independent connections, in each era, receive byte-identical tools/list
// results from the base endpoint. In the modern era a set_profile attempt on
// one of them is refused and changes nothing — there is no session to bind the
// selection to — so the facade stays identical afterwards too.
func TestStatelessList_ToolsListIdenticalAcrossConnections(t *testing.T) {
	env := newProfileTestEnv(t)
	url := unpinnedProxyEndpoint(t, env.proxyServer, env.proxyServer.mcpProxy.GetMCPServer())

	forEachEra(t, func(t *testing.T, era protocolEra) {
		a := newRawEraClient(t, url, era)
		b := newRawEraClient(t, url, era)

		listA := a.result(string(mcp.MethodToolsList), nil)
		a.requireEra(listA)
		listB := b.result(string(mcp.MethodToolsList), nil)
		b.requireEra(listB)

		require.Equal(t, string(listA), string(listB),
			"two %s-era connections must receive a byte-identical tools/list", era.name)
		require.Contains(t, string(listA), `"name":"retrieve_tools"`, "the canonical facade must be the retrieve_tools surface")

		if !a.modern() {
			return
		}
		// Stateless era: the selection cannot be persisted, so the list on this
		// connection — and the other one — is unchanged by the attempt.
		attempt := a.callTool("set_profile", map[string]any{"profile": "research"})
		require.True(t, attempt.IsError, "a modern request must not be able to select a session profile")
		assert.Equal(t, string(listA), string(a.result(string(mcp.MethodToolsList), nil)),
			"a refused set_profile must leave this connection's tools/list unchanged")
		assert.Equal(t, string(listB), string(b.result(string(mcp.MethodToolsList), nil)),
			"a refused set_profile on one connection must not reach another")
	})
}

// ─── the two list filters skip the session tier (T020 unit / T021) ──────────

// statelessListProxy is a bare proxy over two servers and one profile, enough
// for the discovery filters to resolve a stored session selection.
func statelessListProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "research-srv", Enabled: true},
			{Name: "deploy-srv", Enabled: true},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
		},
	}
	return &MCPProxyServer{config: cfg, sessionStore: NewSessionStore(zap.NewNop()), logger: zap.NewNop()}
}

// Direct-mode tools/list is the same for a legacy session that selected a
// profile with set_profile and for a fresh one. Before this change the filter
// consulted the session tier and dropped deploy-srv's tool for the first.
func TestStatelessList_DirectToolsListIgnoresSessionSelection(t *testing.T) {
	proxy := statelessListProxy(t)
	researchTool := FormatDirectToolName("research-srv", "search_papers")
	deployTool := FormatDirectToolName("deploy-srv", "deploy_app")
	publishPermsCatalog(proxy, map[string]string{
		researchTool: auth.PermRead,
		deployTool:   auth.PermRead,
	})
	tools := []mcp.Tool{{Name: researchTool}, {Name: deployTool}}

	proxy.sessionStore.SetActiveProfile("sess-selected", "research")
	selected := sessionCtx(adminCtx(), "sess-selected")
	fresh := sessionCtx(adminCtx(), "sess-fresh")

	withSelection := directToolNamesForTest(proxy.filterDirectModeToolsForAuth(selected, tools))
	withoutSelection := directToolNamesForTest(proxy.filterDirectModeToolsForAuth(fresh, tools))

	assert.Equal(t, []string{researchTool, deployTool}, withoutSelection, "a fresh session lists both servers' tools")
	assert.Equal(t, withoutSelection, withSelection,
		"FR-012: tools/list must not vary with a session's set_profile selection")

	// The call-time re-evaluation of the same filter chain is an execution
	// gate, not a list, and keeps honouring the selection: the deploy tool is
	// withheld (unregistered-name -32602) for the session that chose research.
	callTime := withDirectRequestKindBox(selected)
	setDirectRequestKind(callTime, directRequestKindCall)
	assert.Equal(t, []string{researchTool}, directToolNamesForTest(proxy.filterDirectModeToolsForAuth(callTime, tools)),
		"the session selection still scopes execution")
}

// prompts/list is the same for a legacy session that selected a profile and
// for a fresh one — the pre-existing FR-012 violation research D1 named: on
// main, a base-/mcp session that ran set_profile got a prompts/list that varied
// by connection.
func TestStatelessList_PromptsListIgnoresSessionSelection(t *testing.T) {
	proxy := statelessListProxy(t)
	prompts := []mcp.Prompt{
		{Name: FormatDirectPromptName("research-srv", "summarize"), Meta: stampAggregatedPromptServer(nil, "research-srv")},
		{Name: FormatDirectPromptName("deploy-srv", "plan_rollout"), Meta: stampAggregatedPromptServer(nil, "deploy-srv")},
	}

	proxy.sessionStore.SetActiveProfile("sess-selected", "research")
	selected := sessionCtx(adminCtx(), "sess-selected")
	fresh := sessionCtx(adminCtx(), "sess-fresh")

	withSelection := promptNamesForTest(proxy.filterAggregatedPromptsForAuth(selected, prompts))
	withoutSelection := promptNamesForTest(proxy.filterAggregatedPromptsForAuth(fresh, prompts))

	assert.Len(t, withoutSelection, 2, "a fresh session lists both servers' prompts")
	assert.Equal(t, withoutSelection, withSelection,
		"FR-012: prompts/list must not vary with a session's set_profile selection")

	// prompts/get runs the handler-side gate (Spec 104 FR-016g), which is an
	// execution gate and keeps honouring the selection.
	assert.NoError(t, proxy.authorizeAggregatedPromptServer(selected, "research-srv"))
	assert.ErrorIs(t, proxy.authorizeAggregatedPromptServer(selected, "deploy-srv"), errPromptNotFound,
		"the session selection still scopes prompts/get")
}
