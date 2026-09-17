package server

// audit_funnel_test.go — Spec 107 PR-D (T103/T104): the audit line at the
// dispatch funnels, driven through the real handlers against an in-process
// counting upstream (scope_fixture_test.go). Every line is validated against
// the binding contract schema.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

const auditContractSchemaPath = "../../specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json"

// recordingAuditSink is an in-memory audit.Sink.
type recordingAuditSink struct {
	mu    sync.Mutex
	lines [][]byte
}

func (s *recordingAuditSink) Write(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(line))
	copy(cp, line)
	s.lines = append(s.lines, cp)
	return nil
}

func (s *recordingAuditSink) WriteFailures() uint64 { return 0 }
func (s *recordingAuditSink) Close() error          { return nil }

func (s *recordingAuditSink) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.lines))
	copy(out, s.lines)
	return out
}

// decoded returns every line as a map, in write order, after validating it
// against the contract schema.
func (s *recordingAuditSink) decoded(t *testing.T) []map[string]interface{} {
	t.Helper()
	sch := auditContractSchema(t)
	var out []map[string]interface{}
	for i, raw := range s.snapshot() {
		var m map[string]interface{}
		require.NoErrorf(t, json.Unmarshal(raw, &m), "line %d is not JSON: %s", i, raw)
		var inst interface{}
		require.NoError(t, json.Unmarshal(raw, &inst))
		require.NoErrorf(t, sch.Validate(inst), "line %d violates the contract schema: %s", i, raw)
		out = append(out, m)
	}
	return out
}

var (
	auditSchemaOnce sync.Once
	auditSchema     *jsonschema.Schema
	auditSchemaErr  error
)

func auditContractSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	auditSchemaOnce.Do(func() {
		raw, err := os.ReadFile(auditContractSchemaPath)
		if err != nil {
			auditSchemaErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			auditSchemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("mem://audit-line.schema.json", doc); err != nil {
			auditSchemaErr = err
			return
		}
		auditSchema, auditSchemaErr = c.Compile("mem://audit-line.schema.json")
	})
	require.NoError(t, auditSchemaErr)
	return auditSchema
}

func auditCallToolRequest(name string, args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"name": name, "args": args}
	return req
}

func callerOf(t *testing.T, line map[string]interface{}) map[string]interface{} {
	t.Helper()
	c, ok := line["caller"].(map[string]interface{})
	require.True(t, ok, "line has no caller object: %v", line)
	return c
}

// A high-entropy sentinel that must be byte-absent from every line.
const auditArgSentinel = "SENTINEL-q7Vt9xZp3LmN8kRw2Ye6Hs4Ju1Bc0Df5"

func TestAuditFunnel_AllowedDispatchWritesAuthzAllowThenToolCall(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

	args := map[string]interface{}{"q": auditArgSentinel, "n": float64(3)}
	result, err := proxy.handleCallToolVariant(fullTierAgentOn("a"), auditCallToolRequest("a:erase", args), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError, "control: the call must dispatch")
	require.Equal(t, int64(1), up.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 2, "one authz + one tool_call per dispatched call")

	authz, toolCall := lines[0], lines[1]
	assert.Equal(t, "authz", authz["event"])
	assert.Equal(t, "allow", authz["decision"])
	assert.Equal(t, "none", authz["reason"])
	assert.Nil(t, authz["disclosed"])
	assert.Equal(t, "call_tool_read", authz["surface"])
	assert.Equal(t, "a", authz["server"])
	assert.Equal(t, "erase", authz["tool"])
	assert.Equal(t, "read", authz["operation"])
	assert.Equal(t, "mcp", authz["source"])
	assert.Equal(t, "local", authz["origin"])
	c := callerOf(t, authz)
	assert.Equal(t, "agent_token", c["kind"])
	assert.Equal(t, "mcp_agt_fix", c["token_prefix"])
	assert.NotEmpty(t, c["token_name"])
	assert.Nil(t, c["user_id"], "an ownerless token carries no owner identity")

	assert.Equal(t, "tool_call", toolCall["event"])
	assert.Equal(t, "success", toolCall["outcome"])
	assert.Nil(t, toolCall["reason"])
	assert.Nil(t, toolCall["error_class"])
	assert.Equal(t, authz["request_id"], toolCall["request_id"], "the pair shares the activity request id")
	assert.Equal(t, authz["args_sha256"], toolCall["args_sha256"])

	wantHash, wantBytes, err := audit.HashArgs(args)
	require.NoError(t, err)
	assert.Equal(t, wantHash, authz["args_sha256"])
	assert.EqualValues(t, wantBytes, authz["args_bytes"])

	for _, raw := range sink.snapshot() {
		assert.NotContains(t, string(raw), auditArgSentinel, "an argument value must never reach the line")
	}
}

func TestAuditFunnel_ScopeRefusalRecordsHiddenServerUndisclosed(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	up := startCountingUpstream(t, proxy, rt, "b", readSpec("erase"))

	result, err := proxy.handleCallToolVariant(fullTierAgentOn("a"), auditCallToolRequest("b:erase", map[string]interface{}{"x": auditArgSentinel}), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "not in scope", "the caller's response is unchanged")
	assert.Equal(t, int64(0), up.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 1, "a refusal is exactly one authz line and no tool_call")
	authz := lines[0]
	assert.Equal(t, "authz", authz["event"])
	assert.Equal(t, "deny", authz["decision"])
	assert.Equal(t, "token_scope", authz["reason"])
	assert.Equal(t, false, authz["disclosed"])
	assert.Equal(t, "b", authz["server"], "the hidden server's real name is recorded for the operator")
	assert.Nil(t, authz["outcome"])
	assert.NotContains(t, string(sink.snapshot()[0]), auditArgSentinel)
}

func TestAuditFunnel_UnknownServerIsAuthzDenyNotCallable(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink

	result, err := proxy.handleCallToolVariant(adminCtx(), auditCallToolRequest("zzz:ghost", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)

	lines := sink.decoded(t)
	require.Len(t, lines, 1, "the shared gate refuses a server with no record before any dispatch")
	assert.Equal(t, "deny", lines[0]["decision"])
	assert.Equal(t, "tool_not_callable", lines[0]["reason"])
	assert.Equal(t, true, lines[0]["disclosed"])
	assert.Equal(t, "call_tool_write", lines[0]["surface"])
	assert.Equal(t, "write", lines[0]["operation"])
	assert.Equal(t, "api_key", callerOf(t, lines[0])["kind"])
}

func TestAuditFunnel_NotConnectedIsToolCallErrorUpstreamUnavailable(t *testing.T) {
	// A configured server the proxy holds no client for: every gate passes
	// (no record says otherwise) and the dispatch fails before any upstream.
	proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", URL: "http://127.0.0.1:9/mcp", Protocol: "streamable-http", Enabled: true}))

	result, err := proxy.handleCallToolVariant(adminCtx(), auditCallToolRequest("a:erase", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError)

	lines := sink.decoded(t)
	require.Len(t, lines, 2, "%v", lines)
	assert.Equal(t, "allow", lines[0]["decision"])
	assert.Equal(t, "tool_call", lines[1]["event"])
	assert.Equal(t, "error", lines[1]["outcome"])
	assert.Equal(t, "upstream_unavailable", lines[1]["error_class"])
	assert.Equal(t, lines[0]["request_id"], lines[1]["request_id"])
}

func TestAuditFunnel_IntentInvalidIsAuthzDenyWithUnknownPair(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name":                    "a:erase",
		"intent_data_sensitivity": "not-a-level",
	}
	result, err := proxy.handleCallToolVariant(adminCtx(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError)

	lines := sink.decoded(t)
	require.Len(t, lines, 1)
	assert.Equal(t, "deny", lines[0]["decision"])
	assert.Equal(t, "intent_rejected", lines[0]["reason"])
	assert.Equal(t, true, lines[0]["disclosed"])
}

func TestAuditFunnel_NestedRefusalCarriesParentAndScriptCaller(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	up := startCountingUpstream(t, proxy, rt, "b", readSpec("erase"))

	call := runSandboxCallTool(t, proxy, fullTierAgentOn("a"), "b", "erase")
	require.False(t, call.OK)
	assert.Equal(t, int64(0), up.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 1, "the wrapper writes no line; the nested refusal writes exactly one authz")
	authz := lines[0]
	assert.Equal(t, "authz", authz["event"])
	assert.Equal(t, "deny", authz["decision"])
	assert.Equal(t, "token_scope", authz["reason"])
	assert.Equal(t, false, authz["disclosed"])
	assert.Equal(t, "code_execution", authz["surface"])
	assert.Equal(t, "internal", authz["source"])
	assert.NotEmpty(t, authz["parent_id"])
	assert.Equal(t, "b", authz["server"])
	assert.Equal(t, "erase", authz["tool"])
	c := callerOf(t, authz)
	assert.Equal(t, "agent_token", c["kind"], "nested children keep the script's caller")
	assert.Equal(t, "mcp_agt_fix", c["token_prefix"])
}

func TestAuditFunnel_NestedAllowedDispatchPairsUnderParent(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

	call := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
	require.True(t, call.OK, "control: %s", call.Message)
	assert.Equal(t, int64(1), up.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 2, "one authz allow + one tool_call for the nested call; none for the wrapper")
	authz, toolCall := lines[0], lines[1]
	assert.Equal(t, "allow", authz["decision"])
	assert.Equal(t, "code_execution", authz["surface"])
	assert.Equal(t, "internal", authz["source"])
	assert.NotEmpty(t, authz["parent_id"])
	assert.Equal(t, "read", authz["operation"])
	assert.Equal(t, "api_key", callerOf(t, authz)["kind"], "nested children keep the script's caller")
	assert.Equal(t, "tool_call", toolCall["event"])
	assert.Equal(t, "success", toolCall["outcome"])
	assert.Equal(t, authz["parent_id"], toolCall["parent_id"])
	assert.Equal(t, authz["request_id"], toolCall["request_id"])
}

func TestAuditFunnel_CountInvariantOverManyCalls(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

	const allowed, denied = 40, 20
	var wg sync.WaitGroup
	for i := 0; i < allowed; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = proxy.handleCallToolVariant(fullTierAgentOn("a"), auditCallToolRequest("a:erase", map[string]interface{}{"i": float64(i)}), contracts.ToolVariantRead)
		}(i)
	}
	for i := 0; i < denied; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = proxy.handleCallToolVariant(fullTierAgentOn("a"), auditCallToolRequest("b:erase", nil), contracts.ToolVariantRead)
		}()
	}
	wg.Wait()

	var authzAllow, authzDeny, toolCalls int
	for _, l := range sink.decoded(t) {
		switch l["event"] {
		case "authz":
			if l["decision"] == "allow" {
				authzAllow++
			} else {
				authzDeny++
			}
		case "tool_call":
			toolCalls++
		}
	}
	assert.Equal(t, allowed, authzAllow)
	assert.Equal(t, denied, authzDeny)
	assert.Equal(t, authzAllow, toolCalls, "#tool_call == #authz(allow)")
}

func TestAuditFunnel_NilSinkIsFree(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	require.Nil(t, proxy.auditSink)
	ctx := proxy.installAuditAttempt(context.Background(), auditAttemptSpec{RequestID: "r", Server: "a", Tool: "t"})
	assert.Nil(t, auditDispatchFromContext(ctx), "no sink: no attempt is installed")
	assert.NotPanics(t, func() {
		proxy.auditAuthz(ctx, "deny", "token_scope")
		proxy.auditToolCall(ctx, "success", "", "", 0, nil, nil)
		proxy.emitActivityPolicyDecision(ctx, "a", "t", "", "r", "blocked", "x", "token_scope")
	})
}

func TestAuditFunnel_OutputBlockIsToolCallBlockedNeverSecondAuthz(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.OutputSanitisation = sanCfg("block", true, false)
	proxy.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
	sink := &recordingAuditSink{}
	proxy.auditSink = sink

	ctx := proxy.installAuditAttempt(adminCtx(), auditAttemptSpec{
		RequestID: "req-san-block", Server: "github", Tool: "get_secret",
		Operation: "read", Surface: contracts.ToolVariantRead,
	})
	// The dispatch path wrote the allow line at Started, before the upstream.
	proxy.emitActivityToolCallStarted(ctx, "github", "get_secret", "", "req-san-block", "mcp", nil)

	fwd := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.TextContent{Type: "text", Text: "leaked " + awsKeyFixture},
	}}
	block := proxy.applyOutputSanitisation(ctx, "github", "get_secret", "req-san-block", contracts.ContentTrustUntrusted, fwd)
	require.NotNil(t, block)

	// Defence in depth: were a completion emitted after the block, it must
	// not add a second tool_call.
	proxy.emitActivityToolCallCompleted(ctx, "github", "get_secret", "", "req-san-block", "mcp", "error", "blocked", 1, nil, "", false, "", nil, "", "", 0, 0, "", nil, "")

	lines := sink.decoded(t)
	require.Len(t, lines, 2, "%v", lines)
	assert.Equal(t, "authz", lines[0]["event"])
	assert.Equal(t, "allow", lines[0]["decision"])
	assert.Equal(t, "tool_call", lines[1]["event"])
	assert.Equal(t, "blocked", lines[1]["outcome"])
	assert.Equal(t, "output_sanitisation", lines[1]["reason"])
	assert.Nil(t, lines[1]["error_class"])
	for _, raw := range sink.snapshot() {
		assert.NotContains(t, string(raw), awsKeyFixture)
	}
}

func TestAuditFunnel_LimiterShedIsToolCallRejected(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink

	for reason, want := range map[limiter.Reason]string{
		limiter.ReasonQueueFull:    "limiter_queue_full",
		limiter.ReasonQueueTimeout: "limiter_queue_timeout",
	} {
		ctx := proxy.installAuditAttempt(adminCtx(), auditAttemptSpec{
			RequestID: "req-shed-" + string(reason), Server: "db", Tool: "query", Operation: "read", Surface: contracts.ToolVariantRead,
		})
		proxy.emitActivityToolCallStarted(ctx, "db", "query", "", "req-shed", "mcp", nil)
		proxy.auditToolCallShed(ctx, &limiter.LimitError{Scope: limiter.ScopeServer, Reason: reason, Server: "db", Limit: 2}, 5)

		lines := sink.decoded(t)
		require.Len(t, lines, 2)
		assert.Equal(t, "allow", lines[0]["decision"])
		assert.Equal(t, "rejected", lines[1]["outcome"])
		assert.Equal(t, want, lines[1]["reason"])
		assert.Equal(t, "api_key", callerOf(t, lines[1])["kind"])
		sink.mu.Lock()
		sink.lines = nil
		sink.mu.Unlock()
	}
}

func TestAuditFunnel_StdioCallerAndSocketOrigin(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	sink := &recordingAuditSink{}
	proxy.auditSink = sink

	stdio := proxy.installAuditAttempt(stdioAuthContext(context.Background()), auditAttemptSpec{RequestID: "r1", Server: "a", Tool: "t", Operation: "read", Surface: contracts.ToolVariantRead})
	proxy.auditAuthz(stdio, "deny", telemetry.BlockReasonServerQuarantined)

	tray := transport.TagConnectionContext(adminCtx(), transport.ConnectionSourceTray)
	tray = proxy.installAuditAttempt(tray, auditAttemptSpec{RequestID: "r2", Server: "a", Tool: "t", Operation: "read", Surface: contracts.ToolVariantRead})
	proxy.auditAuthz(tray, "deny", telemetry.BlockReasonProfileScope)

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "stdio", callerOf(t, lines[0])["kind"])
	assert.Equal(t, "local", lines[0]["origin"])
	assert.Equal(t, "server_quarantined", lines[0]["reason"])
	assert.Equal(t, true, lines[0]["disclosed"])
	assert.Equal(t, "socket", callerOf(t, lines[1])["kind"])
	assert.Equal(t, "socket", lines[1]["origin"])
	assert.Equal(t, false, lines[1]["disclosed"])
}
