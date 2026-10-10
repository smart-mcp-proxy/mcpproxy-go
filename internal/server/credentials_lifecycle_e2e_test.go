package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 115 Phase 5 (quickstart.md §3): end-to-end proof of the credential
// lifecycle over real HTTP MCP sessions against an in-process daemon. All
// administration in the success paths is MCP (the `profiles` and `credentials`
// tools); the only REST calls are read-only sink captures and, in the guard
// persistence scenario, the API funnel used as the attack vector. Every denied
// or revoked call asserts an inner semantic refusal AND a zero dispatch delta
// on the counting mock upstreams. Preflight is never consulted (#1548).

const credE2EAPIKey = "test-api-key-e2e-credentials-0123456789"

type credE2E struct {
	t       *testing.T
	env     *TestEnvironment
	library *MockUpstreamServer
	tracker *MockUpstreamServer
	base    string // http://127.0.0.1:<port>
	logs    *observer.ObservedLogs
	admin   *client.Client
}

func startCredMock(t *testing.T, name string, tools []mcp.Tool) *MockUpstreamServer {
	t.Helper()
	srv := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	mock := &MockUpstreamServer{server: srv, tools: tools}
	for i := range tools {
		tool := tools[i]
		srv.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mock.countDispatch(tool.Name)
			return mcp.NewToolResultText(fmt.Sprintf(`{"server":%q,"tool":%q,"ok":true}`, name, tool.Name)), nil
		})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	mock.addr = "http://" + ln.Addr().String()
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(srv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	mock.stopFunc = func() error { return httpSrv.Shutdown(context.Background()) }
	t.Cleanup(func() { _ = mock.stopFunc() })
	return mock
}

func credE2ETools() (library, tracker []mcp.Tool) {
	library = []mcp.Tool{
		mcp.NewTool("search_books", mcp.WithDescription("Search the library catalog"), mcp.WithReadOnlyHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false)),
		mcp.NewTool("read_private_notes", mcp.WithDescription("Read private notes"), mcp.WithReadOnlyHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false)),
		mcp.NewTool("add_note", mcp.WithDescription("Add a note"), mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false)),
		{Name: "mystery_tool", Description: "A tool with no annotations", InputSchema: mcp.ToolInputSchema{Type: "object"}},
	}
	tracker = []mcp.Tool{
		mcp.NewTool("list_issues", mcp.WithDescription("List issues"), mcp.WithReadOnlyHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false)),
		mcp.NewTool("comment_issue", mcp.WithDescription("Comment on an issue"), mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false)),
	}
	return
}

func newCredE2E(t *testing.T, mutate func(*config.Config)) *credE2E {
	t.Helper()
	libTools, trTools := credE2ETools()
	e := &credE2E{t: t, library: startCredMock(t, "library", libTools), tracker: startCredMock(t, "tracker", trTools)}
	core, logs := observer.New(zapcore.DebugLevel)
	e.logs = logs
	f := false
	e.env = NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) {
			cfg.APIKey = credE2EAPIKey
			cfg.RequireMCPAuth = true
			cfg.Servers = []*config.ServerConfig{
				{Name: "library", URL: e.library.addr, Protocol: "streamable-http", Enabled: true, Quarantined: false},
				{Name: "tracker", URL: e.tracker.addr, Protocol: "streamable-http", Enabled: true, Quarantined: false},
			}
			cfg.QuarantineEnabled = &f
			cfg.ToolsLimit = 15
			cfg.CallToolTimeout = config.Duration(2 * time.Minute)
			cfg.EnableCodeExecution = true
			if mutate != nil {
				mutate(cfg)
			}
		},
		Logger: func(*config.Config) (*zap.Logger, error) { return zap.New(core), nil },
	})
	t.Cleanup(e.env.Cleanup)
	e.base = strings.TrimSuffix(e.env.proxyAddr, "/mcp")
	e.admin = e.session("/mcp", map[string]string{"X-API-Key": credE2EAPIKey})
	// Wait until both upstreams are connected and indexed: an admin read
	// dispatches once (counted in the baseline, never asserted on).
	last := ""
	for _, name := range []string{"tracker:list_issues", "library:search_books"} {
		name := name
		ok := assert.Eventually(t, func() bool {
			res, err := e.admin.CallTool(context.Background(), callReq("call_tool_read", map[string]any{"name": name, "args": map[string]any{}}))
			if err != nil {
				last = err.Error()
				return false
			}
			last = getToolResultText(res)
			return !res.IsError
		}, 60*time.Second, 500*time.Millisecond)
		if !ok {
			t.Fatalf("upstream tool %s never became callable: %s", name, last)
		}
	}
	_ = last
	return e
}

func callReq(name string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

// session opens one MCP session with the given headers and keeps it open.
func (e *credE2E) session(route string, headers map[string]string) *client.Client {
	e.t.Helper()
	opts := []transport.StreamableHTTPCOption{}
	if headers != nil {
		opts = append(opts, transport.WithHTTPHeaders(headers))
	}
	tr, err := transport.NewStreamableHTTP(e.base+route, opts...)
	require.NoError(e.t, err)
	c := client.NewClient(tr)
	e.env.ConnectClient(c)
	e.t.Cleanup(func() { _ = c.Close() })
	return c
}

func (e *credE2E) adminCall(name string, args map[string]any) (map[string]any, bool, string) {
	e.t.Helper()
	res, err := e.admin.CallTool(context.Background(), callReq(name, args))
	require.NoError(e.t, err)
	text := getToolResultText(res)
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out, res.IsError, text
}

func (e *credE2E) adminOK(name string, args map[string]any) map[string]any {
	e.t.Helper()
	out, isErr, text := e.adminCall(name, args)
	if isErr {
		for _, entry := range e.logs.All() {
			if entry.Level < zapcore.WarnLevel {
				continue
			}
			e.t.Logf("daemon error log: %s %v", entry.Message, entry.ContextMap())
		}
	}
	require.False(e.t, isErr, "%s %v: %s", name, args, text)
	return out
}

func (e *credE2E) total() int64 {
	var n int64
	for _, m := range []*MockUpstreamServer{e.library, e.tracker} {
		for _, tl := range m.tools {
			n += m.Dispatches(tl.Name)
		}
	}
	return n
}

// call runs a worker tools/call and returns (isError, text, transport error).
func workerCall(c *client.Client, name string, args map[string]any) (bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, callReq(name, args))
	if err != nil {
		return true, "", err
	}
	return res.IsError, getToolResultText(res), nil
}

// assertRefusedNoDispatch asserts an inner semantic refusal (isError, or a
// transport error) and a zero total dispatch delta.
func (e *credE2E) assertRefusedNoDispatch(label string, fn func() (bool, string, error)) {
	e.t.Helper()
	before := e.total()
	isErr, text, err := fn()
	assert.True(e.t, isErr || err != nil, "%s must be refused, got: %s", label, text)
	assert.Equal(e.t, before, e.total(), "%s dispatched upstream", label)
}

// assertHiddenToolRefused: a forged credentials call by a caller the tool is
// hidden from is refused as an unknown tool (transport "tool not found" or the
// handler's "unknown tool: credentials"), with zero dispatches.
func (e *credE2E) assertHiddenToolRefused(label string, c *client.Client, args map[string]any) {
	e.t.Helper()
	before := e.total()
	isErr, text, err := workerCall(c, "credentials", args)
	switch {
	case err != nil:
		assert.Contains(e.t, err.Error(), "not found", label)
	default:
		assert.True(e.t, isErr, label)
		assert.Contains(e.t, text, "unknown tool: credentials", label)
	}
	assert.Equal(e.t, before, e.total(), label)
}

func (e *credE2E) toolNames(c *client.Client) []string {
	e.t.Helper()
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(e.t, err)
	names := []string{}
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// rawPost sends one JSON-RPC POST with the credential (and the session id when
// the transport kept one) and returns status and body.
func (e *credE2E) rawPost(route, credential, sessionID, method string) (int, string) {
	e.t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":99,"method":%q,"params":{}}`, method)
	req, err := http.NewRequest(http.MethodPost, e.base+route, strings.NewReader(body))
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("X-API-Key", credential)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (e *credE2E) createResearchProfiles() {
	f := false
	_ = f
	e.adminOK("profiles", map[string]any{"operation": "create", "name": "daily-research", "servers": []any{"library"},
		"max_tier": "read", "unannotated": "deny", "tools": map[string]any{"deny": []any{"library:read_private*"}},
		"code_execution": false, "management_tools": false})
}

// --- E2E-1 -----------------------------------------------------------------------

func TestE2E_CredentialsLifecycle_FreshClient(t *testing.T) {
	e := newCredE2E(t, nil)
	assert.Contains(t, e.toolNames(e.admin), "credentials")
	assert.Contains(t, e.toolNames(e.admin), "profiles")
	e.createResearchProfiles()
	e.adminOK("profiles", map[string]any{"operation": "create", "name": "other", "servers": []any{"library", "tracker"}})

	out := e.adminOK("credentials", map[string]any{"operation": "create_client", "client": "delegated-worker",
		"profile": "daily-research", "expires_in": "1h", "purpose": "Summarise today's library additions; assumes no writes needed"})
	view := out["client"].(map[string]any)
	assert.Equal(t, "locked", view["binding"])
	assert.Equal(t, true, view["lease"])
	assert.Equal(t, "active", view["state"])
	secret := out["credential"].(string)
	require.True(t, strings.HasPrefix(secret, "mcp_cli_"))
	assert.Equal(t, "X-API-Key", out["snippet"].(map[string]any)["header_name"])
	rawLinks, _ := json.Marshal(out["links"])
	assert.NotContains(t, strings.ToLower(string(rawLinks)), "apikey")

	worker := e.session("/mcp", map[string]string{"X-API-Key": secret})
	names := e.toolNames(worker)
	assert.NotContains(t, names, "credentials")
	assert.NotContains(t, names, "profiles")

	before := e.library.Dispatches("search_books")
	isErr, text, err := workerCall(worker, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
	require.NoError(t, err)
	require.False(t, isErr, text)
	assert.Equal(t, before+1, e.library.Dispatches("search_books"), "an admitted read dispatches exactly once")

	for _, c := range []struct {
		label string
		name  string
		args  map[string]any
	}{
		{"write", "call_tool_write", map[string]any{"name": "library:add_note", "args": map[string]any{}}},
		{"private read", "call_tool_read", map[string]any{"name": "library:read_private_notes", "args": map[string]any{}}},
		{"other server", "call_tool_read", map[string]any{"name": "tracker:list_issues", "args": map[string]any{}}},
		{"unannotated", "call_tool_read", map[string]any{"name": "library:mystery_tool", "args": map[string]any{}}},
		{"credentials", "credentials", map[string]any{"operation": "list"}},
		{"profiles", "profiles", map[string]any{"operation": "list"}},
		{"set_profile escape", "set_profile", map[string]any{"profile": "other"}},
	} {
		c := c
		e.assertRefusedNoDispatch(c.label, func() (bool, string, error) { return workerCall(worker, c.name, c.args) })
	}

	rev := e.adminOK("credentials", map[string]any{"operation": "revoke", "client": "delegated-worker"})
	assert.Equal(t, true, rev["changed"])
	// The worker's next request on the SAME session fails with 401.
	before = e.total()
	_, _, err = workerCall(worker, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
	require.Error(t, err, "a revoked session must not call")
	status, body := e.rawPost("/mcp", secret, worker.GetSessionId(), "tools/list")
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, body, "Agent token invalid: token has been revoked")
	assert.Equal(t, before, e.total())

	got := e.adminOK("credentials", map[string]any{"operation": "get", "client": "delegated-worker"})
	cred := got["credential"].(map[string]any)
	assert.Equal(t, "revoked", cred["state"])
	assert.NotEmpty(t, cred["revoked_at"])
}

// --- E2E-2 -----------------------------------------------------------------------

func TestE2E_CredentialsLifecycle_FreshToken(t *testing.T) {
	e := newCredE2E(t, nil)
	e.createResearchProfiles()
	out := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "research-task-42", "profile": "daily-research", "expires_in": "30m"})
	tok := out["token"].(map[string]any)
	assert.Equal(t, "pinned", tok["binding"])
	assert.Equal(t, true, tok["lease"])
	secret := out["credential"].(string)
	require.True(t, strings.HasPrefix(secret, "mcp_agt_"))

	worker := e.session("/mcp", map[string]string{"X-API-Key": secret})
	// Each real outcome matches `profiles explain token=…`.
	for _, tc := range []struct{ server, tool, variant string }{
		{"library", "search_books", "call_tool_read"},
		{"library", "read_private_notes", "call_tool_read"},
		{"library", "add_note", "call_tool_write"},
		{"library", "mystery_tool", "call_tool_read"},
		{"tracker", "list_issues", "call_tool_read"},
	} {
		explained := e.adminOK("profiles", map[string]any{"operation": "explain", "token": "research-task-42", "tool": tc.server + ":" + tc.tool})
		allowed := explained["verdict"] == "allowed"
		before := e.total()
		isErr, text, err := workerCall(worker, tc.variant, map[string]any{"name": tc.server + ":" + tc.tool, "args": map[string]any{}})
		require.NoError(t, err)
		assert.Equal(t, allowed, !isErr, "%s:%s explain=%v call=%s", tc.server, tc.tool, explained["verdict"], text)
		want := int64(0)
		if allowed {
			want = 1
		}
		assert.Equal(t, before+want, e.total(), "%s:%s dispatch delta", tc.server, tc.tool)
	}

	e.adminOK("credentials", map[string]any{"operation": "revoke", "token": "research-task-42"})
	status, body := e.rawPost("/mcp", secret, worker.GetSessionId(), "tools/list")
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, body, "token has been revoked")
}

func TestE2E_CredentialsLifecycle_TokenExpiry(t *testing.T) {
	e := newCredE2E(t, nil)
	e.createResearchProfiles()
	out := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "research-task-43", "profile": "daily-research", "expires_in": "3s"})
	secret := out["credential"].(string)
	worker := e.session("/mcp", map[string]string{"X-API-Key": secret})
	isErr, text, err := workerCall(worker, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
	require.NoError(t, err)
	require.False(t, isErr, text)
	require.Eventually(t, func() bool {
		status, body := e.rawPost("/mcp", secret, worker.GetSessionId(), "tools/list")
		return status == http.StatusUnauthorized && strings.Contains(body, "token has expired")
	}, 10*time.Second, 250*time.Millisecond, "no admin action: the lease ends on its own")
	got := e.adminOK("credentials", map[string]any{"operation": "get", "token": "research-task-43"})
	assert.Equal(t, "expired", got["credential"].(map[string]any)["state"])
}

// --- E2E-3 -----------------------------------------------------------------------

func TestE2E_CredentialsLifecycle_LiveReassign(t *testing.T) {
	e := newCredE2E(t, nil)
	e.adminOK("profiles", map[string]any{"operation": "create", "name": "research", "servers": []any{"tracker"}, "max_tier": "read"})
	e.adminOK("profiles", map[string]any{"operation": "create", "name": "triage", "servers": []any{"tracker"}, "max_tier": "read",
		"tools": map[string]any{"allow": []any{"tracker:comment_issue"}}})
	w1 := e.adminOK("credentials", map[string]any{"operation": "create_client", "client": "w1", "profile": "research", "expires_in": "1h"})["credential"].(string)
	t1 := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "t1", "profile": "research", "expires_in": "1h"})["credential"].(string)
	ws := e.session("/mcp", map[string]string{"X-API-Key": w1})
	ts := e.session("/mcp", map[string]string{"X-API-Key": t1})
	comment := map[string]any{"name": "tracker:comment_issue", "args": map[string]any{}}
	e.assertRefusedNoDispatch("w1 comment before", func() (bool, string, error) { return workerCall(ws, "call_tool_write", comment) })
	e.assertRefusedNoDispatch("t1 comment before", func() (bool, string, error) { return workerCall(ts, "call_tool_write", comment) })

	e.adminOK("profiles", map[string]any{"operation": "assign", "client": "w1", "profile": "triage"})

	before := e.tracker.Dispatches("comment_issue")
	isErr, text, err := workerCall(ws, "call_tool_write", comment)
	require.NoError(t, err)
	require.False(t, isErr, "same session, new grant: %s", text)
	assert.Equal(t, before+1, e.tracker.Dispatches("comment_issue"))
	isErr, text, err = workerCall(ws, "call_tool_read", map[string]any{"name": "tracker:list_issues", "args": map[string]any{}})
	require.NoError(t, err)
	require.False(t, isErr, text)
	e.assertRefusedNoDispatch("t1 keeps research", func() (bool, string, error) { return workerCall(ts, "call_tool_write", comment) })

	eff := e.adminOK("profiles", map[string]any{"operation": "effective_tools", "name": "triage"})
	raw, _ := json.Marshal(eff)
	assert.Contains(t, string(raw), "comment_issue")
}

// --- E2E-4 -----------------------------------------------------------------------

func TestE2E_CredentialsLifecycle_Negatives(t *testing.T) {
	e := newCredE2E(t, nil)
	e.createResearchProfiles()
	cli := e.adminOK("credentials", map[string]any{"operation": "create_client", "client": "neg-c", "profile": "daily-research", "expires_in": "1h"})["credential"].(string)
	tok := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "neg-t", "profile": "daily-research", "expires_in": "1h"})["credential"].(string)

	// Non-admin kinds neither see nor can call the tool; nothing changes.
	listBefore := e.adminOK("credentials", map[string]any{"operation": "list"})["total"]
	for label, secret := range map[string]string{"client": cli, "token": tok} {
		s := e.session("/mcp", map[string]string{"X-API-Key": secret})
		assert.NotContains(t, e.toolNames(s), "credentials", label)
		// Over the wire mcp-go's tool filter (re-run at tools/call) answers a
		// hidden tool exactly like a nonexistent one, as for `profiles`; the
		// handler's own `unknown tool: credentials` text is pinned by
		// TestCredentialsTool_VisibilityMatchesProfiles.
		e.assertHiddenToolRefused(label, s, map[string]any{"operation": "create_token", "name": "forged-" + label, "profile": "daily-research", "expires_in": "1h"})
	}
	// An admin session that set_profile'd into a non-management profile.
	adm2 := e.session("/mcp", map[string]string{"X-API-Key": credE2EAPIKey})
	_, err := adm2.CallTool(context.Background(), callReq("set_profile", map[string]any{"profile": "daily-research"}))
	require.NoError(t, err)
	e.assertHiddenToolRefused("admin on a non-management profile", adm2, map[string]any{"operation": "list"})
	_, err = adm2.CallTool(context.Background(), callReq("set_profile", map[string]any{"profile": ""}))
	require.NoError(t, err)
	isErr, text, _ := workerCall(adm2, "credentials", map[string]any{"operation": "list"})
	assert.False(t, isErr, text)
	assert.Equal(t, listBefore, e.adminOK("credentials", map[string]any{"operation": "list"})["total"])

	// Duplicates, invalid input, conflicts: structured codes, no partial grant.
	for _, c := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"operation": "create_client", "client": "neg-c", "profile": "daily-research", "expires_in": "1h"}, "identity_exists"},
		{map[string]any{"operation": "create_token", "name": "client-foo", "profile": "daily-research", "expires_in": "1h"}, "reserved_identity"},
		{map[string]any{"operation": "create_client", "client": "cursor", "profile": "daily-research", "expires_in": "1h"}, "reserved_identity"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "", "expires_in": "1h"}, "profile_required"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-reserch", "expires_in": "1h"}, "unknown_profile"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "1y"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "-1h"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "0s"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "400d"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "106752d"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "9999999999d"}, "invalid_expiry"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research"}, "missing_argument"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "1h", "allowed_servers": "*"}, "invalid_argument"},
		{map[string]any{"operation": "create_token", "name": "x1", "profile": "daily-research", "expires_in": "1h", "purpose": map[string]any{"x": []any{tok}}}, "secret_in_argument"},
	} {
		out, isErr, text := e.adminCall("credentials", c.args)
		require.True(t, isErr, "%v: %s", c.args, text)
		assert.Equal(t, c.code, out["code"], "%v", c.args)
	}
	assert.Equal(t, listBefore, e.adminOK("credentials", map[string]any{"operation": "list"})["total"], "no refusal leaves a credential")

	// 20 concurrent issues of one name: exactly one wins.
	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.admin.CallTool(context.Background(), callReq("credentials", map[string]any{"operation": "create_token", "name": "same-name", "profile": "daily-research", "expires_in": "1h"}))
			if err != nil {
				results <- "transport"
				return
			}
			var out map[string]any
			_ = json.Unmarshal([]byte(getToolResultText(res)), &out)
			if !res.IsError {
				results <- "ok"
				return
			}
			results <- fmt.Sprint(out["code"])
		}()
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for r := range results {
		counts[r]++
	}
	assert.Equal(t, map[string]int{"ok": 1, "identity_exists": 19}, counts)

	// Deleted pin fails closed and is reported dangling.
	ts := e.session("/mcp", map[string]string{"X-API-Key": tok})
	e.adminOK("profiles", map[string]any{"operation": "delete", "name": "daily-research", "force": true})
	e.assertRefusedNoDispatch("dangling pin", func() (bool, string, error) {
		return workerCall(ts, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
	})
	got := e.adminOK("credentials", map[string]any{"operation": "get", "token": "neg-t"})
	assert.Equal(t, "dangling", got["credential"].(map[string]any)["profile_state"])

	// With the token still active, turning auth off through the API funnel is
	// refused naming it (FR-012a/b).
	req, err := http.NewRequest(http.MethodPatch, e.base+"/api/v1/config", strings.NewReader(`{"require_mcp_auth":false}`))
	require.NoError(t, err)
	req.Header.Set("X-API-Key", credE2EAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))
	assert.Contains(t, string(raw), "binding_bypassable_without_auth")
	assert.Contains(t, string(raw), "neg-t")
}

func TestE2E_CredentialsLifecycle_WriteGates(t *testing.T) {
	e := newCredE2E(t, nil)
	e.createResearchProfiles()
	e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "pre", "profile": "daily-research", "expires_in": "1h"})
	live := e.env.proxyServer.runtime.Config()
	for _, gate := range []struct {
		set  func(bool)
		code string
	}{
		{func(v bool) { live.ReadOnlyMode = v }, "read_only_mode"},
		{func(v bool) { live.DisableManagement = v }, "management_disabled"},
	} {
		gate.set(true)
		for _, args := range []map[string]any{
			{"operation": "create_client", "client": "g", "profile": "daily-research", "expires_in": "1h"},
			{"operation": "create_token", "name": "g", "profile": "daily-research", "expires_in": "1h"},
			{"operation": "revoke", "token": "pre"},
		} {
			out, isErr, text := e.adminCall("credentials", args)
			require.True(t, isErr, text)
			assert.Equal(t, gate.code, out["code"])
		}
		e.adminOK("credentials", map[string]any{"operation": "list"})
		e.adminOK("credentials", map[string]any{"operation": "get", "token": "pre"})
		gate.set(false)
	}
	got := e.adminOK("credentials", map[string]any{"operation": "get", "token": "pre"})
	assert.Equal(t, "active", got["credential"].(map[string]any)["state"])
	assert.EqualValues(t, 1, e.adminOK("credentials", map[string]any{"operation": "list"})["total"])
}

// --- E2E-4 surface parity -------------------------------------------------------------

func TestE2E_CredentialsLifecycle_SurfaceParity(t *testing.T) {
	e := newCredE2E(t, nil)
	e.adminOK("profiles", map[string]any{"operation": "create", "name": "daily-research-code", "servers": []any{"library"},
		"max_tier": "read", "unannotated": "deny", "tools": map[string]any{"deny": []any{"library:read_private*"}},
		"code_execution": true, "management_tools": false})
	e.createResearchProfiles()
	tok := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "par-t", "profile": "daily-research-code", "expires_in": "1h"})["credential"].(string)
	cli := e.adminOK("credentials", map[string]any{"operation": "create_client", "client": "par-c", "profile": "daily-research-code", "expires_in": "1h"})["credential"].(string)
	forbidden := []string{"library:add_note", "library:read_private_notes", "library:mystery_tool", "tracker:list_issues"}
	for label, secret := range map[string]string{"token": tok, "client": cli} {
		hdr := map[string]string{"X-API-Key": secret}
		for _, route := range []string{"/mcp", "/mcp/call"} {
			s := e.session(route, hdr)
			before := e.library.Dispatches("search_books")
			isErr, text, err := workerCall(s, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
			require.NoError(t, err)
			require.False(t, isErr, "%s %s: %s", label, route, text)
			assert.Equal(t, before+1, e.library.Dispatches("search_books"))
			for _, name := range forbidden {
				name := name
				e.assertRefusedNoDispatch(label+" "+route+" "+name, func() (bool, string, error) {
					return workerCall(s, "call_tool_write", map[string]any{"name": name, "args": map[string]any{}})
				})
			}
		}
		// /mcp/code: admitted nested read dispatches once, forbidden nested calls zero.
		code := e.session("/mcp/code", hdr)
		before := e.library.Dispatches("search_books")
		isErr, text, err := workerCall(code, "code_execution", map[string]any{"code": `const r = call_tool('library', 'search_books', {}); ({ok: !r.isError})`})
		require.NoError(t, err)
		require.False(t, isErr, "%s nested read: %s", label, text)
		assert.Equal(t, before+1, e.library.Dispatches("search_books"))
		for _, name := range forbidden {
			srv, tool, _ := strings.Cut(name, ":")
			script := fmt.Sprintf(`const r = call_tool(%q, %q, {}); ({refused: !!r.isError || !!r.error})`, srv, tool)
			b := e.total()
			_, _, _ = workerCall(code, "code_execution", map[string]any{"code": script})
			assert.Equal(t, b, e.total(), "%s nested %s dispatched", label, name)
		}
		// /mcp/all direct: admitted read once, forbidden zero.
		direct := e.session("/mcp/all", hdr)
		before = e.library.Dispatches("search_books")
		isErr, text, err = workerCall(direct, "library__search_books", map[string]any{})
		require.NoError(t, err)
		require.False(t, isErr, "%s direct read: %s", label, text)
		assert.Equal(t, before+1, e.library.Dispatches("search_books"))
		for _, name := range forbidden {
			direct := strings.Replace(name, ":", "__", 1)
			e.assertRefusedNoDispatch(label+" direct "+name, func() (bool, string, error) { return workerCall(e.session("/mcp/all", hdr), direct, map[string]any{}) })
		}
		assert.NotContains(t, e.toolNames(direct), "credentials")
	}

	// Code-disabled profile: a forged code_execution is refused before it runs.
	off := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "par-off", "profile": "daily-research", "expires_in": "1h"})["credential"].(string)
	code := e.session("/mcp/code", map[string]string{"X-API-Key": off})
	assert.NotContains(t, e.toolNames(code), "code_execution")
	e.assertRefusedNoDispatch("forged code_execution", func() (bool, string, error) {
		return workerCall(code, "code_execution", map[string]any{"code": `call_tool('library', 'search_books', {})`})
	})
}

// --- E2E-5 -----------------------------------------------------------------------

// sseCapture streams /events into a buffer until stopped.
type sseCapture struct {
	mu   sync.Mutex
	buf  strings.Builder
	stop context.CancelFunc
	done chan struct{}
}

func (e *credE2E) captureSSE() *sseCapture {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &sseCapture{stop: cancel, done: make(chan struct{})}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/events", http.NoBody)
	require.NoError(e.t, err)
	req.Header.Set("X-API-Key", credE2EAPIKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	go func() {
		defer close(c.done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<22)
		for sc.Scan() {
			c.mu.Lock()
			c.buf.WriteString(sc.Text())
			c.buf.WriteByte('\n')
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *sseCapture) text() string {
	c.stop()
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (e *credE2E) restGet(path string) string {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.base+path, http.NoBody)
	require.NoError(e.t, err)
	req.Header.Set("X-API-Key", credE2EAPIKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw)
}

func TestE2E_CredentialsLifecycle_SecretSinks(t *testing.T) {
	e := newCredE2E(t, nil)
	sse := e.captureSSE()
	e.createResearchProfiles()
	var notes []string
	var notesMu sync.Mutex
	e.admin.OnNotification(func(n mcp.JSONRPCNotification) {
		raw, _ := json.Marshal(n)
		notesMu.Lock()
		notes = append(notes, string(raw))
		notesMu.Unlock()
	})

	cliOut := e.adminOK("credentials", map[string]any{"operation": "create_client", "client": "sink-c", "profile": "daily-research", "expires_in": "1h", "purpose": "sink test"})
	tokOut := e.adminOK("credentials", map[string]any{"operation": "create_token", "name": "sink-t", "profile": "daily-research", "expires_in": "1h"})
	cs, ts := cliOut["credential"].(string), tokOut["credential"].(string)
	secrets := []string{cs, ts, credE2EAPIKey}

	var errorsSeen []string
	// list/get/revoke and a failed duplicate for both kinds.
	for _, args := range []map[string]any{
		{"operation": "list"}, {"operation": "get", "client": "sink-c"}, {"operation": "get", "token": "sink-t"},
		{"operation": "create_client", "client": "sink-c", "profile": "daily-research", "expires_in": "1h"},
		{"operation": "create_token", "name": "sink-t", "profile": "daily-research", "expires_in": "1h"},
	} {
		_, _, text := e.adminCall("credentials", args)
		errorsSeen = append(errorsSeen, text)
	}
	// Secrets fed back into every free-text and invalid-input path.
	for _, s := range secrets {
		for _, args := range []map[string]any{
			{"operation": "create_client", "client": "x", "profile": "daily-research", "expires_in": "1h", "purpose": s},
			{"operation": "create_client", "client": "x", "profile": "daily-research", "expires_in": "1h", "display_name": "w " + s},
			{"operation": "create_client", "client": s, "profile": "daily-research", "expires_in": "1h"},
			{"operation": "create_token", "name": s, "profile": "daily-research", "expires_in": "1h"},
			{"operation": "create_token", "name": "x", "profile": s, "expires_in": "1h"},
			{"operation": "create_token", "name": "x", "profile": "daily-research", "expires_in": s},
			{"operation": s},
			{"operation": "list", "unknown_key": s},
		} {
			out, isErr, text := e.adminCall("credentials", args)
			require.True(t, isErr, text)
			assert.Equal(t, "secret_in_argument", out["code"])
			errorsSeen = append(errorsSeen, text)
		}
	}
	e.adminOK("credentials", map[string]any{"operation": "revoke", "client": "sink-c"})
	e.adminOK("credentials", map[string]any{"operation": "revoke", "token": "sink-t"})
	time.Sleep(500 * time.Millisecond) // let the async activity writer settle

	sinks := map[string]string{
		"errors":          strings.Join(errorsSeen, "\n"),
		"activity":        e.restGet("/api/v1/activity?limit=1000"),
		"export json":     e.restGet("/api/v1/activity/export?format=json"),
		"export csv":      e.restGet("/api/v1/activity/export?format=csv"),
		"tokens list":     e.restGet("/api/v1/tokens"),
		"clients list":    e.restGet("/api/v1/clients"),
		"credentials get": fmt.Sprint(e.adminOK("credentials", map[string]any{"operation": "list"})),
	}
	notesMu.Lock()
	sinks["notifications"] = strings.Join(notes, "\n")
	notesMu.Unlock()
	var logBuf strings.Builder
	for _, entry := range e.logs.All() {
		logBuf.WriteString(entry.Message)
		for _, f := range entry.Context {
			fmt.Fprintf(&logBuf, " %s=%v %s", f.Key, f.Interface, f.String)
		}
		logBuf.WriteByte('\n')
	}
	sinks["logs"] = logBuf.String()
	cfgPath := config.GetConfigPath(e.env.proxyServer.runtime.Config().DataDir)
	if raw, err := os.ReadFile(cfgPath); err == nil {
		sinks["config file"] = string(raw)
	}
	if raw, err := os.ReadFile(filepath.Join(e.env.proxyServer.runtime.Config().DataDir, "config.db")); err == nil {
		sinks["config.db"] = string(raw)
	}
	sinks["sse"] = sse.text()
	assert.Contains(t, sinks["sse"], "credentials.changed", "the SSE capture saw the lifecycle events")
	assert.Contains(t, sinks["activity"], "credentials", "the activity capture is not empty")
	for name, sink := range sinks {
		for _, s := range []string{cs, ts} {
			assert.NotContains(t, sink, s, "issued secret leaked into %s", name)
		}
		if name != "config file" {
			assert.NotContains(t, sink, credE2EAPIKey, "API key leaked into %s", name)
		}
	}

	// A revoked credential cannot read on a fresh request.
	status, _ := e.rawPost("/mcp", cs, "", "tools/list")
	assert.Equal(t, http.StatusUnauthorized, status)
}

// T026a (A19): revoke wins over an in-flight connect. A supported client holds
// a current secret and, mid-reconnect, a staged secret; an MCP revoke answers
// changed:true at once, both live sessions get 401 on their next request, no
// call dispatches afterwards, and the connect fails closed at commit.
func TestE2E_CredentialsLifecycle_RevokeDuringConnect(t *testing.T) {
	e := newCredE2E(t, nil)
	e.createResearchProfiles()
	minter := e.env.proxyServer.runtime.ClientsService().ConnectMinter()
	prof := "daily-research"
	intent := connect.CredentialIntent{Profile: &prof, ActorKind: "api_key", Surface: "api"}
	first, err := minter.Issue("cursor", intent)
	require.NoError(t, err)
	require.NoError(t, minter.Commit("cursor", intent, first))
	current := e.session("/mcp", map[string]string{"X-API-Key": first.Secret})
	isErr, text, err := workerCall(current, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
	require.NoError(t, err)
	require.False(t, isErr, text)

	// A reconnect stages a second secret and holds the in-flight claim.
	staged, err := minter.Issue("cursor", intent)
	require.NoError(t, err)
	require.True(t, staged.Rotating)
	stagedSession := e.session("/mcp", map[string]string{"X-API-Key": staged.Secret})

	rev := e.adminOK("credentials", map[string]any{"operation": "revoke", "client": "cursor"})
	assert.Equal(t, true, rev["changed"], "a held connect claim never blocks a revoke")
	assert.Equal(t, true, rev["client_config_untouched"])

	before := e.total()
	for label, c := range map[string]struct {
		secret string
		sess   *client.Client
	}{"current": {first.Secret, current}, "staged": {staged.Secret, stagedSession}} {
		status, body := e.rawPost("/mcp", c.secret, c.sess.GetSessionId(), "tools/list")
		assert.Equal(t, http.StatusUnauthorized, status, label)
		assert.Contains(t, body, "revoked", label)
		_, _, err := workerCall(c.sess, "call_tool_read", map[string]any{"name": "library:search_books", "args": map[string]any{}})
		assert.Error(t, err, label)
	}
	assert.Equal(t, before, e.total(), "nothing dispatches after the revoke")

	var superseded *runtime.CredentialSupersededError
	require.ErrorAs(t, minter.Commit("cursor", intent, staged), &superseded)
	for _, secret := range []string{first.Secret, staged.Secret} {
		status, _ := e.rawPost("/mcp", secret, "", "tools/list")
		assert.Equal(t, http.StatusUnauthorized, status, "the failed commit restores nothing")
	}
	got := e.adminOK("credentials", map[string]any{"operation": "get", "client": "cursor"})
	assert.Equal(t, "revoked", got["credential"].(map[string]any)["state"])
}
