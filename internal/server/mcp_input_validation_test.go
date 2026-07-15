package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// Spec 085 US3 — self-healing failed calls (contracts/invalid-params-error.md):
//   - T030 (FR-013 Path A): a call_tool_* invocation whose args fail the
//     stored input schema returns a typed invalid_params error embedding the
//     tool's FULL input schema + a one-line hint, WITHOUT dispatching upstream
//     (a counting stub records zero invocations). A corrected retry succeeds.
//   - T030 (FR-013b): an uncompilable stored schema fails OPEN — the call
//     dispatches exactly as today and the validation_skipped counter ticks.
//   - T031 (FR-013 scenario 2): non-argument failures (auth 401, HTTP 5xx,
//     timeout, JSON-RPC internal error) keep the existing
//     createDetailedErrorResponse shapes — never an input_schema.
//   - T034 (FR-013 Path B): upstream JSON-RPC -32602 / best-effort
//     invalid-params messages attach the schema through the same renderer.
//   - US3 scenario 3: the error is byte-identical under full and compact
//     tool_response_mode (mode independence).

const stubEchoSchema = `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"}},"required":["title"]}`

// startCountingStubUpstream starts an in-process streamable-HTTP MCP server
// exposing one tool per (name, schema) pair, wires it into the proxy's
// upstream manager as server "stub", and returns a per-tool invocation
// counter. The counter is the T030 "upstream stub records zero invocations"
// witness.
func startCountingStubUpstream(t *testing.T, proxy *MCPProxyServer, tools map[string]mcp.ToolInputSchema) *atomic.Int64 {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	// The post-dispatch bookkeeping (tool-call records) needs a mainServer —
	// unit proxies default to nil because most tests never dispatch. Same
	// pattern as the cross-surface consistency tests.
	if proxy.mainServer == nil {
		proxy.mainServer = newConsistencyServer(t)
	}

	var calls atomic.Int64
	mcpSrv := mcpserver.NewMCPServer("stub", "1.0.0-test", mcpserver.WithToolCapabilities(true))
	for name, schema := range tools {
		tool := mcp.Tool{Name: name, Description: "Stub tool " + name, InputSchema: schema}
		mcpSrv.AddTool(tool, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			raw, _ := json.Marshal(map[string]interface{}{"ok": true, "args": request.Params.Arguments})
			return mcp.NewToolResultText(string(raw)), nil
		})
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := fmt.Sprintf("http://%s", ln.Addr().String())
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })

	serverCfg := &config.ServerConfig{
		Name: "stub", URL: addr, Protocol: "streamable-http", Enabled: true,
	}
	require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))
	require.NoError(t, proxy.upstreamManager.AddServerConfig("stub", serverCfg))
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))

	require.Eventually(t, func() bool {
		client, ok := proxy.upstreamManager.GetClient("stub")
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond, "stub upstream must connect")

	return &calls
}

// indexStubTool indexes metadata for stub:<name> so the pre-dispatch
// validator has a schema source (the same index signatures render from).
func indexStubTool(t *testing.T, proxy *MCPProxyServer, name, paramsJSON string) {
	t.Helper()
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "stub:" + name, ServerName: "stub",
		Description: "Stub tool " + name,
		ParamsJSON:  paramsJSON,
		Hash:        "hash-stub-" + name,
	}))
}

// callVariant drives handleCallToolVariant with args_json.
func callVariant(t *testing.T, proxy *MCPProxyServer, ctx context.Context, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name":      toolName,
		"args_json": string(argsJSON),
	}
	result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// decodeErrorBody parses the JSON error body of an error result.
func decodeErrorBody(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.True(t, result.IsError, "expected an error result, got: %v", result.Content)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &body), "error body must be JSON: %s", text.Text)
	return body
}

// T030 (FR-013 Path A): missing required arg ⇒ typed invalid_params error with
// the full input_schema + hint, upstream NOT called; the schema-informed
// retry succeeds and reaches the stub exactly once.
func TestPreDispatchValidation_MissingRequiredArg_SelfHealing(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	calls := startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"echo": {
			Type: "object",
			Properties: map[string]interface{}{
				"title": map[string]interface{}{"type": "string"},
				"body":  map[string]interface{}{"type": "string"},
			},
			Required: []string{"title"},
		},
	})
	indexStubTool(t, proxy, "echo", stubEchoSchema)

	// Invalid call: required "title" omitted.
	result := callVariant(t, proxy, context.Background(), "stub:echo", map[string]interface{}{"body": "x"})
	body := decodeErrorBody(t, result)

	assert.Equal(t, "invalid_params", body["error_type"], "typed error (contract)")
	assert.Equal(t, "stub:echo", body["tool"])
	assert.Contains(t, body["error"], "invalid arguments for stub:echo",
		"error line names the failing tool")
	assert.Contains(t, body["error"], "title", "error names the missing property")

	require.Contains(t, body, "input_schema", "the FULL stored schema must be embedded")
	schemaJSON, err := json.Marshal(body["input_schema"])
	require.NoError(t, err)
	assert.JSONEq(t, stubEchoSchema, string(schemaJSON),
		"input_schema is the tool's full stored ParamsJSON, verbatim")

	hint, _ := body["hint"].(string)
	assert.Contains(t, hint, "describe_tool", "hint points at the second stage")
	assert.Contains(t, hint, "stub:echo", "hint names the failing tool id")

	assert.Equal(t, int64(0), calls.Load(),
		"upstream must NOT be dispatched on a pre-dispatch validation failure (FR-013)")

	// Schema-informed retry: corrected args succeed and reach the stub once.
	retry := callVariant(t, proxy, context.Background(), "stub:echo", map[string]interface{}{"title": "hi", "body": "x"})
	assert.False(t, retry.IsError, "corrected retry must succeed: %v", retry.Content)
	assert.Equal(t, int64(1), calls.Load(), "valid call dispatches exactly once")
}

// T030 (FR-013 Path A): a wrong-typed arg (not just a missing one) is also
// caught pre-dispatch.
func TestPreDispatchValidation_WrongType(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	calls := startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"echo": {Type: "object", Properties: map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
		}, Required: []string{"title"}},
	})
	indexStubTool(t, proxy, "echo", stubEchoSchema)

	result := callVariant(t, proxy, context.Background(), "stub:echo", map[string]interface{}{"title": float64(42)})
	body := decodeErrorBody(t, result)
	assert.Equal(t, "invalid_params", body["error_type"])
	assert.Contains(t, body, "input_schema")
	assert.Equal(t, int64(0), calls.Load(), "upstream must not see the invalid call")
}

// T030 (FR-013b): an uncompilable stored schema ⇒ fail-open — the call
// dispatches exactly as today and the validation_skipped counter ticks.
// Validation must never block a call a schemaless proxy would have allowed.
func TestPreDispatchValidation_FailOpen_UncompilableSchema(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	calls := startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"loose": {Type: "object", Properties: map[string]interface{}{}},
	})
	// Truncated JSON: cannot even parse, let alone compile.
	indexStubTool(t, proxy, "loose", `{"type":"obj`)

	before := proxy.inputValidator.SkippedCount()
	result := callVariant(t, proxy, context.Background(), "stub:loose", map[string]interface{}{"anything": true})
	assert.False(t, result.IsError, "fail-open: dispatch must proceed (FR-013b), got: %v", result.Content)
	assert.Equal(t, int64(1), calls.Load(), "the call must reach the upstream")
	assert.Equal(t, before+1, proxy.inputValidator.SkippedCount(),
		"validation_skipped must be counted (FR-013b)")
}

// T030 (FR-013b edge): an unsupported-construct schema (valid JSON, invalid
// JSON Schema) also fails open.
func TestPreDispatchValidation_FailOpen_InvalidSchemaConstruct(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	calls := startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"weird": {Type: "object", Properties: map[string]interface{}{}},
	})
	// "type": 12 is valid JSON but not a compilable JSON Schema.
	indexStubTool(t, proxy, "weird", `{"type":12}`)

	before := proxy.inputValidator.SkippedCount()
	result := callVariant(t, proxy, context.Background(), "stub:weird", map[string]interface{}{"x": 1})
	assert.False(t, result.IsError, "fail-open: dispatch must proceed, got: %v", result.Content)
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, before+1, proxy.inputValidator.SkippedCount())
}

// T030: a tool with no stored schema (empty ParamsJSON) dispatches unvalidated
// — that is not a "skip" (nothing was skipped; there was nothing to check).
func TestPreDispatchValidation_NoSchema_DispatchesWithoutSkipCount(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	calls := startCountingStubUpstream(t, proxy, map[string]mcp.ToolInputSchema{
		"bare": {Type: "object", Properties: map[string]interface{}{}},
	})
	indexStubTool(t, proxy, "bare", "")

	before := proxy.inputValidator.SkippedCount()
	result := callVariant(t, proxy, context.Background(), "stub:bare", map[string]interface{}{"x": 1})
	assert.False(t, result.IsError)
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, before, proxy.inputValidator.SkippedCount(),
		"no-schema is not a validation skip")
}

// US3 scenario 3 (SC-006 mode independence): the identical failing call under
// tool_response_mode full and compact produces byte-identical errors — the
// self-healing path never consults the response mode. No upstream client is
// needed: validation fires before the connection check by construction.
func TestPreDispatchValidation_ModeIndependent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	failingCall := func() string {
		result := callVariant(t, proxy, context.Background(), "github:create_issue",
			map[string]interface{}{"body": "no title"})
		require.True(t, result.IsError)
		return result.Content[0].(mcp.TextContent).Text
	}

	proxy.config.ToolResponseMode = config.ToolResponseModeFull
	fullErr := failingCall()

	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	compactErr := failingCall()

	assert.Equal(t, fullErr, compactErr,
		"invalid_params error must be byte-identical in both response modes (US3 scenario 3)")

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(fullErr), &body))
	assert.Equal(t, "invalid_params", body["error_type"])
	assert.Contains(t, body, "input_schema")
}

// T031 (FR-013 scenario 2): non-argument failures keep the EXISTING
// createDetailedErrorResponse shapes — no input_schema, no invalid_params
// error_type — even when the failing tool has a stored schema.
func TestCreateDetailedErrorResponse_NonArgumentFailures_NoSchema(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy) // github:create_issue HAS a schema in the index

	cases := []struct {
		name string
		err  error
	}{
		{"auth 401", &transport.HTTPError{StatusCode: 401, Body: "Unauthorized", URL: "http://up", Method: "POST"}},
		{"http 500", &transport.HTTPError{StatusCode: 500, Body: "Internal Server Error", URL: "http://up", Method: "POST"}},
		{"json-rpc internal error", &transport.JSONRPCError{Code: -32603, Message: "internal error"}},
		{"timeout", errors.New("context deadline exceeded")},
		{"connection refused", errors.New("dial tcp 127.0.0.1:9: connect: connection refused")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := proxy.createDetailedErrorResponse(c.err, "github", "create_issue")
			body := decodeErrorBody(t, result)
			assert.NotContains(t, body, "input_schema",
				"non-argument failures must never attach a schema (FR-013 scenario 2)")
			assert.NotEqual(t, "invalid_params", body["error_type"])
		})
	}
}

// T034 (FR-013 Path B): an upstream JSON-RPC -32602 is classified as
// invalid-params and gets the schema + hint through the same renderer Path A
// uses.
func TestCreateDetailedErrorResponse_UpstreamInvalidParams_AttachesSchema(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	rpcErr := &transport.JSONRPCError{Code: -32602, Message: "Invalid params: missing required property 'title'"}
	result := proxy.createDetailedErrorResponse(rpcErr, "github", "create_issue")
	body := decodeErrorBody(t, result)

	assert.Equal(t, "invalid_params", body["error_type"])
	assert.Equal(t, "github:create_issue", body["tool"])
	assert.Contains(t, body["error"], "invalid arguments for github:create_issue")
	require.Contains(t, body, "input_schema")
	schemaJSON, err := json.Marshal(body["input_schema"])
	require.NoError(t, err)
	// The stored ParamsJSON of github:create_issue in seedEntryBuilderFixture.
	assert.JSONEq(t,
		`{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"labels":{"type":"array","items":{"type":"string"}},"ttl":{"type":"integer","default":3600}},"required":["title"]}`,
		string(schemaJSON))
	assert.Contains(t, body["hint"], "describe_tool")
}

// T034 (FR-013 Path B, best-effort): a plain-string upstream error that
// clearly reads as an argument-validation failure is classified too.
func TestCreateDetailedErrorResponse_BestEffortInvalidParamsMessage(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	err := errors.New("tool call failed: invalid arguments: missing required parameter 'title'")
	result := proxy.createDetailedErrorResponse(err, "github", "create_issue")
	body := decodeErrorBody(t, result)

	assert.Equal(t, "invalid_params", body["error_type"])
	assert.Contains(t, body, "input_schema")
	assert.Contains(t, body["hint"], "describe_tool")
}

// Path B classifier unit table (FR-013 scenario 2): the untyped best-effort
// branch must be narrow. Only unambiguous schema-validation phrasing
// classifies; anything that smells like transport/auth/timeout — including
// upstream auth errors that merely CONTAIN "invalid parameters", like
// "401 Unauthorized: invalid parameters" — must keep its existing shape.
func TestClassifyUpstreamInvalidParams_Table(t *testing.T) {
	positive := []struct {
		name string
		err  error
	}{
		{"typed -32602", &transport.JSONRPCError{Code: -32602, Message: "Invalid params"}},
		{"missing required parameter", errors.New("missing required parameter 'title'")},
		{"missing required property", errors.New("tool call failed: missing required property 'name'")},
		{"missing required argument", errors.New("missing required argument: query")},
		{"missing required field", errors.New("missing required field 'id'")},
		{"required property phrasing", errors.New("required property 'title' not provided")},
		{"does not match schema", errors.New("arguments does not match schema for tool echo")},
		{"validation failed for parameter", errors.New("validation failed for parameter 'limit'")},
	}
	for _, c := range positive {
		t.Run("positive/"+c.name, func(t *testing.T) {
			detail, ok := classifyUpstreamInvalidParams(c.err)
			assert.True(t, ok, "must classify as invalid-params: %v", c.err)
			assert.NotEmpty(t, detail)
		})
	}

	negative := []struct {
		name string
		err  error
	}{
		// The reported false positive: an auth failure carrying the old broad phrase.
		{"401 unauthorized with invalid parameters", errors.New("401 Unauthorized: invalid parameters")},
		{"bare invalid parameters", errors.New("invalid parameters")},
		{"bare invalid params", errors.New("upstream rejected request: invalid params")},
		{"bare invalid arguments", errors.New("invalid arguments")},
		{"forbidden", errors.New("403 Forbidden: missing required parameter scope")},
		{"auth wording wins over schema wording", errors.New("authentication failed: missing required parameter 'token'")},
		{"token wording", errors.New("token expired; missing required parameter refresh")},
		{"oauth wording", errors.New("oauth handshake failed: required property missing")},
		{"timeout", errors.New("request timeout: validation failed for parameter 'q'")},
		{"deadline", errors.New("context deadline exceeded")},
		{"connection", errors.New("connection reset by peer")},
		{"dial", errors.New("dial tcp 127.0.0.1:9: connect: refused")},
		{"tls", errors.New("tls: handshake failure")},
		{"http status shape", errors.New("HTTP 400: missing required parameter 'title'")},
		{"status code shape", errors.New("request failed with status code 422: does not match schema")},
		{"bare 4xx code", errors.New("upstream returned 422: missing required parameter 'title'")},
		{"bare 5xx code", errors.New("500 internal error: required property lost")},
		{"typed HTTP error", &transport.HTTPError{StatusCode: 400, Body: "missing required parameter 'title'", URL: "http://up", Method: "POST"}},
		{"typed json-rpc non--32602", &transport.JSONRPCError{Code: -32603, Message: "missing required parameter 'title'"}},
	}
	for _, c := range negative {
		t.Run("negative/"+c.name, func(t *testing.T) {
			_, ok := classifyUpstreamInvalidParams(c.err)
			assert.False(t, ok, "must NOT classify as invalid-params: %v", c.err)
		})
	}
}

// The 401 false positive end-to-end: even for a tool WITH a stored schema, an
// auth-flavored string error must keep the generic shape — no input_schema,
// no invalid_params reclassification.
func TestCreateDetailedErrorResponse_AuthStringError_NotReclassified(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy) // github:create_issue HAS a schema in the index

	result := proxy.createDetailedErrorResponse(
		errors.New("401 Unauthorized: invalid parameters"), "github", "create_issue")
	body := decodeErrorBody(t, result)
	assert.NotContains(t, body, "input_schema",
		"an upstream auth error must never be reclassified as invalid_params")
	assert.NotEqual(t, "invalid_params", body["error_type"])
}

// T034 (Path B degradation): a -32602 for a tool with NO stored schema keeps
// the existing JSON-RPC error shape — a self-healing error without a schema
// would be an empty promise.
func TestCreateDetailedErrorResponse_InvalidParamsWithoutStoredSchema_KeepsShape(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	// Nothing indexed: no schema source.

	rpcErr := &transport.JSONRPCError{Code: -32602, Message: "Invalid params"}
	result := proxy.createDetailedErrorResponse(rpcErr, "ghost", "vanished")
	body := decodeErrorBody(t, result)

	assert.NotContains(t, body, "input_schema")
	assert.NotEqual(t, "invalid_params", body["error_type"])
	assert.Equal(t, float64(-32602), body["error_code"], "existing JSON-RPC shape preserved")
}
