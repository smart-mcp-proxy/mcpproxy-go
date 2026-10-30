package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// This independent upstream advertises one strict, static schema for every
// tool. It implements JSON-RPC over real HTTP, including genuine -32602
// responses, rather than replacing the proxy's client or transport with a mock.
const argumentPrivacySchema = `{"type":"object","additionalProperties":false,"properties":{"text":{"type":"string","minLength":2,"maxLength":16,"description":"Static text description retained in full"},"kind":{"type":"string","enum":["one","two"]},"payload":{"type":"object","additionalProperties":false,"properties":{}}},"required":["text","kind"]}`

var argumentPrivacyRPCControls = []struct {
	name string
	code int
}{
	{"rpc_parse", mcp.PARSE_ERROR},
	{"rpc_request", mcp.INVALID_REQUEST},
	{"rpc_method", mcp.METHOD_NOT_FOUND},
	{"internal", mcp.INTERNAL_ERROR},
	{"rpc_interrupted", mcp.REQUEST_INTERRUPTED},
	{"rpc_resource", mcp.RESOURCE_NOT_FOUND},
}

type argumentPrivacyUpstream struct {
	url        string
	requests   atomic.Int64
	handlers   atomic.Int64
	initialize atomic.Int64
	lists      atomic.Int64
	beforeFail atomic.Pointer[func()]
}

func newArgumentPrivacyUpstream(t *testing.T) *argumentPrivacyUpstream {
	t.Helper()
	u := &argumentPrivacyUpstream{}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name            string                 `json:"name"`
				Arguments       map[string]interface{} `json:"arguments"`
				ProtocolVersion string                 `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		response := map[string]interface{}{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			u.initialize.Add(1)
			response["result"] = map[string]interface{}{
				"protocolVersion": request.Params.ProtocolVersion,
				"serverInfo":      map[string]string{"name": "privacy-fixture", "version": "1"},
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			}
		case "tools/list":
			u.lists.Add(1)
			var tools []mcp.Tool
			names := []string{"check", "reject", "generic"}
			for _, control := range argumentPrivacyRPCControls {
				names = append(names, control.name)
			}
			for _, name := range names {
				tools = append(tools, mcp.NewToolWithRawSchema(name, "Independent privacy fixture", json.RawMessage(argumentPrivacySchema)))
			}
			response["result"] = map[string]interface{}{"tools": tools}
		case "tools/call":
			// This counts parsed HTTP tools/call requests independently of
			// entry into the actual tool handler below. Handshake/list traffic
			// cannot make a zero-dispatch assertion pass or fail.
			u.requests.Add(1)
			result, rpcError := u.call(request.Params.Name, request.Params.Arguments)
			if rpcError != nil {
				response["error"] = rpcError
			} else {
				response["result"] = result
			}
		case "ping":
			response["result"] = map[string]interface{}{}
		default:
			response["error"] = map[string]interface{}{"code": -32601, "message": "Unknown fixture method"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(hs.Close)
	u.url = hs.URL
	return u
}

func (u *argumentPrivacyUpstream) call(name string, args map[string]interface{}) (*mcp.CallToolResult, map[string]interface{}) {
	u.handlers.Add(1)
	for _, control := range argumentPrivacyRPCControls {
		if name == control.name {
			return nil, map[string]interface{}{
				"code": control.code, "message": "validation failed for argument 'text': " + fmt.Sprint(args["text"]),
			}
		}
	}
	if name == "reject" || name == "generic" {
		if beforeFail := u.beforeFail.Load(); beforeFail != nil {
			(*beforeFail)()
		}
		message := fmt.Sprintf("validation failed for argument 'text': %v at /%v", args["text"], args["text"])
		if name == "reject" && args["kind"] == "two" {
			message = "401 Unauthorized: " + message
		}
		if name == "generic" {
			message = fmt.Sprintf("Invalid params: %v", args["text"])
		}
		return nil, map[string]interface{}{
			"code": -32602, "message": message,
			"data": map[string]interface{}{"rejected": args["text"], "nested": []interface{}{args}},
		}
	}
	b, _ := json.Marshal(map[string]interface{}{"fixture": "owned-loopback", "args": args})
	return mcp.NewToolResultText(string(b)), nil
}

type argumentPrivacyEnv struct {
	proxy    *MCPProxyServer
	client   *client.Client
	upstream *argumentPrivacyUpstream
}

func newArgumentPrivacyEnv(t *testing.T) *argumentPrivacyEnv {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	u := newArgumentPrivacyUpstream(t)
	p := createTestMCPProxyServer(t)
	// Successful dispatch bookkeeping uses the real Server runtime, with
	// tokenization disabled and temporary data. No supervisor is started.
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Tokenizer = &config.TokenizerConfig{Enabled: false}
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown()) })
	p.mainServer = srv
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, p.upstreamManager.ShutdownAll(ctx))
		require.NoError(t, p.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, item := range []struct{ name, mode string }{
		{"private", config.ToolArgumentErrorModeRedacted}, {"default", ""}, {"detailed", config.ToolArgumentErrorModeDetailed},
	} {
		sc := &config.ServerConfig{Name: item.name, URL: u.url, Protocol: "streamable-http", Enabled: true, ToolArgumentErrorMode: item.mode}
		require.NoError(t, p.storage.SaveUpstreamServer(sc))
		require.NoError(t, p.upstreamManager.AddServerConfig(item.name, sc))
		upstream, ok := p.upstreamManager.GetClient(item.name)
		require.True(t, ok)
		require.NoError(t, upstream.Connect(ctx))
	}
	metadata, err := p.upstreamManager.DiscoverTools(ctx)
	require.NoError(t, err)
	for _, meta := range metadata {
		require.NoError(t, p.index.IndexTool(meta))
	}
	hs := httptest.NewServer(mcpserver.NewStreamableHTTPServer(p.GetMCPServer()))
	t.Cleanup(hs.Close)
	tr, err := clienttransport.NewStreamableHTTP(hs.URL)
	require.NoError(t, err)
	c := client.NewClient(tr)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	require.NoError(t, c.Start(ctx))
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "argument-privacy-test", Version: "1"}
	_, err = c.Initialize(ctx, req)
	require.NoError(t, err)
	return &argumentPrivacyEnv{proxy: p, client: c, upstream: u}
}

func privacyValidArgs() map[string]interface{} {
	return map[string]interface{}{"text": "safe", "kind": "one", "payload": map[string]interface{}{}}
}

func (e *argumentPrivacyEnv) setResponseMode(t *testing.T, mode string) {
	t.Helper()
	// The attached runtime owns currentConfig; changing construction-time
	// p.config alone would leave the compact cases running in full mode.
	cfg := *e.proxy.currentConfig()
	cfg.ToolResponseMode = mode
	e.proxy.mainServer.runtime.UpdateConfig(&cfg, "")
	require.Equal(t, mode, e.proxy.effectiveToolResponseMode(""))
}

func (e *argumentPrivacyEnv) call(t *testing.T, server, tool, encoding string, args map[string]interface{}, legacy bool) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = "call_tool_read"
	params := map[string]interface{}{"name": server + ":" + tool}
	if encoding == "args_json" {
		b, err := json.Marshal(args)
		require.NoError(t, err)
		params["args_json"] = string(b)
	} else {
		params["args"] = args
	}
	req.Params.Arguments = params
	var result *mcp.CallToolResult
	var err error
	if legacy {
		// The legacy handler is not registered on any public MCP mount.
		// Exercise its real outbound HTTP path without registering a new API.
		result, err = e.proxy.handleCallTool(ctx, req)
	} else {
		result, err = e.client.CallTool(ctx, req)
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func privacyAssertNoMarkers(t *testing.T, result *mcp.CallToolResult, markers ...string) {
	t.Helper()
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	surfaces := []string{string(wire)}
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			surfaces = append(surfaces, tc.Text)
			var decoded interface{}
			if json.Unmarshal([]byte(tc.Text), &decoded) == nil {
				b, err := json.Marshal(decoded)
				require.NoError(t, err)
				surfaces = append(surfaces, string(b))
			}
		}
	}
	for _, marker := range markers {
		escaped, err := json.Marshal(marker)
		require.NoError(t, err)
		pointer := strings.ReplaceAll(strings.ReplaceAll(marker, "~", "~0"), "/", "~1")
		var unicodeEscaped strings.Builder
		for _, r := range marker {
			if r < 128 {
				unicodeEscaped.WriteRune(r)
			} else {
				for _, unit := range utf16.Encode([]rune{r}) {
					fmt.Fprintf(&unicodeEscaped, `\u%04x`, unit)
				}
			}
		}
		unicodeJSON, err := json.Marshal(unicodeEscaped.String())
		require.NoError(t, err)
		for _, surface := range surfaces {
			for _, form := range []string{marker, string(escaped[1 : len(escaped)-1]), pointer, unicodeEscaped.String(), string(unicodeJSON[1 : len(unicodeJSON)-1])} {
				require.NotContains(t, surface, form, "request marker leaked through a public result")
			}
		}
	}
}

func privacyAssertIndexedError(t *testing.T, result *mcp.CallToolResult, toolID string, redacted bool) {
	t.Helper()
	body := decodeErrorBody(t, result)
	require.Len(t, body, 5, "preserve exactly the established indexed error fields")
	require.Equal(t, "invalid_params", body["error_type"])
	require.Equal(t, toolID, body["tool"])
	require.Equal(t, fmt.Sprintf("Fix arguments to match input_schema, then retry. For the full definition call describe_tool({tool_ids:[%q]}).", toolID), body["hint"])
	b, err := json.Marshal(body["input_schema"])
	require.NoError(t, err)
	require.JSONEq(t, argumentPrivacySchema, string(b), "full static schema, including nested restrictions/descriptions")
	if redacted {
		require.Equal(t, "Invalid tool arguments.", body["error"])
	} else {
		require.Contains(t, body["error"], "invalid arguments for "+toolID+":")
	}
}

func privacyAssertUpstreamError(t *testing.T, result *mcp.CallToolResult, server, marker string, redacted bool) {
	t.Helper()
	if redacted {
		privacyAssertIndexedError(t, result, server+":reject", true)
		privacyAssertNoMarkers(t, result, marker)
		return
	}
	// This is the actual pre-refinement generic envelope observed in the
	// original HTTP red receipt, including the proxy-authored auth suffix.
	require.Equal(t, map[string]interface{}{
		"error":       fmt.Sprintf("tool 'reject' on server '%s' failed: CallTool failed for 'reject': invalid params: validation failed for argument 'text': %s at /%s. Check server configuration, authentication, and tool parameters", server, marker, marker),
		"server_name": server, "tool_name": "reject",
		"troubleshooting": "Check server configuration, connectivity, and authentication credentials",
	}, decodeErrorBody(t, result))
}

// Bounded helper sanity: actual SDK/public proxy, initialize/list wire schema,
// successful upstream dispatch and independent HTTP/handler counts.
func TestArgumentPrivacyHTTPFixture(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	meta := e.proxy.lookupIndexedTool("private", "check")
	require.NotNil(t, meta)
	require.JSONEq(t, argumentPrivacySchema, meta.ParamsJSON)
	require.GreaterOrEqual(t, e.upstream.initialize.Load(), int64(3))
	require.GreaterOrEqual(t, e.upstream.lists.Load(), int64(3))
	require.Zero(t, e.upstream.requests.Load())
	require.Zero(t, e.upstream.handlers.Load())
	result := e.call(t, "private", "check", "args", privacyValidArgs(), false)
	require.False(t, result.IsError)
	require.Contains(t, fwdText(result), "owned-loopback")
	require.Equal(t, int64(1), e.upstream.requests.Load())
	require.Equal(t, int64(1), e.upstream.handlers.Load())
}

// ARG-PXY-01: every invalid request is followed immediately by a real valid
// request. Delta counters exclude previous cases and handshake/list traffic.
func TestArgumentPrivacyHTTPPreDispatch(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	key, value, path := "badK_雪/~", "badV_🙂", "badP_雪/~"
	cases := []struct {
		name       string
		edit       func(map[string]interface{})
		diagnostic string
	}{
		{"unknown_ascii", func(a map[string]interface{}) { a["badKeyASCII"] = value }, "badKeyASCII"},
		{"unknown_unicode_pointer", func(a map[string]interface{}) { a[key] = value }, key},
		{"nested_pointer", func(a map[string]interface{}) { a["payload"] = map[string]interface{}{path: value} }, path},
		{"wrong_type", func(a map[string]interface{}) { a["text"] = map[string]interface{}{key: value} }, "text"},
		{"enum", func(a map[string]interface{}) { a["kind"] = value }, "kind"},
		{"length", func(a map[string]interface{}) { a["text"] = value + strings.Repeat("x", 20) }, "text"},
	}
	for _, mode := range []string{"full", "compact"} {
		e.setResponseMode(t, mode)
		for _, server := range []string{"private", "default", "detailed"} {
			for _, encoding := range []string{"args", "args_json"} {
				for _, tc := range cases {
					t.Run(mode+"/"+server+"/"+encoding+"/"+tc.name, func(t *testing.T) {
						args := privacyValidArgs()
						tc.edit(args)
						beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
						result := e.call(t, server, "check", encoding, args, false)
						privacyAssertIndexedError(t, result, server+":check", server == "private")
						if server == "private" {
							privacyAssertNoMarkers(t, result, key, value, path, "badKeyASCII")
						} else {
							require.NotEqual(t, "Invalid tool arguments.", decodeErrorBody(t, result)["error"])
							require.Contains(t, decodeErrorBody(t, result)["error"], tc.diagnostic, "default/detailed retain the specific diagnostic")
						}
						require.Equal(t, beforeHTTP, e.upstream.requests.Load(), "invalid pre-dispatch must send zero HTTP tools/call")
						require.Equal(t, beforeHandler, e.upstream.handlers.Load(), "invalid pre-dispatch must enter zero handlers")
						retry := e.call(t, server, "check", encoding, privacyValidArgs(), false)
						require.False(t, retry.IsError, "%v", retry.Content)
						require.Contains(t, fwdText(retry), "owned-loopback")
						require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
						require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
					})
				}
			}
		}
	}
}

// ARG-PXY-02: mcp-go v1.0.0 loses the numeric code/data in AsError. Real
// -32602 messages qualify here through the existing narrow schema allowlist.
// The separate generic-message case pins the existing noneligible boundary.
func TestArgumentPrivacyHTTPUpstream(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	marker := "rpcM_雪/~"
	for _, mode := range []string{"full", "compact"} {
		e.setResponseMode(t, mode)
		for _, server := range []string{"private", "default", "detailed"} {
			for _, legacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/legacy=%t", mode, server, legacy), func(t *testing.T) {
					args := privacyValidArgs()
					args["text"] = marker
					beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
					result := e.call(t, server, "reject", "args_json", args, legacy)
					privacyAssertUpstreamError(t, result, server, marker, server == "private")
					require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
					require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
				})
			}
		}
	}
	t.Run("generic_sdk_invalid_params_remains_noneligible", func(t *testing.T) {
		args := privacyValidArgs()
		args["text"] = marker
		beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
		body := decodeErrorBody(t, e.call(t, "private", "generic", "args", args, false))
		require.Contains(t, body["error"], marker)
		require.NotContains(t, body, "input_schema")
		require.NotContains(t, body, "hint")
		require.NotContains(t, body, "error_code", "SDK erased the code before the renderer")
		require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
		require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
	})
}

// Changing persisted policy after the real request has captured its gate
// cannot change that request's pre-dispatch response. The callback is the
// existing deterministic request boundary; assertions concern the public SDK
// result and real persisted state, not the callback's private implementation.
func TestArgumentPrivacyHTTPCapturedPreDispatchPolicy(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	marker := "capturedK_雪/~"
	for _, change := range []struct{ before, after string }{
		{config.ToolArgumentErrorModeRedacted, config.ToolArgumentErrorModeDetailed},
		{"", config.ToolArgumentErrorModeRedacted},
		{config.ToolArgumentErrorModeDetailed, config.ToolArgumentErrorModeRedacted},
	} {
		t.Run(change.before+"_to_"+change.after, func(t *testing.T) {
			sc, err := e.proxy.storage.GetUpstreamServer("private")
			require.NoError(t, err)
			sc.ToolArgumentErrorMode = change.before
			require.NoError(t, e.proxy.storage.SaveUpstreamServer(sc))
			changed := make(chan error, 1)
			e.proxy.dispatchGatePause = func(server, tool string) {
				if server == "private" && tool == "check" {
					updated := *sc
					updated.ToolArgumentErrorMode = change.after
					changed <- e.proxy.storage.SaveUpstreamServer(&updated)
				}
			}
			args := privacyValidArgs()
			args[marker] = "capturedV_🙂"
			beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
			result := e.call(t, "private", "check", "args", args, false)
			e.proxy.dispatchGatePause = nil
			select {
			case err := <-changed:
				require.NoError(t, err)
			default:
				t.Fatal("real request did not cross the captured-policy boundary")
			}
			persisted, err := e.proxy.storage.GetUpstreamServer("private")
			require.NoError(t, err)
			require.Equal(t, change.after, persisted.ToolArgumentErrorMode)
			privacyAssertIndexedError(t, result, "private:check", change.before == config.ToolArgumentErrorModeRedacted)
			if change.before == config.ToolArgumentErrorModeRedacted {
				privacyAssertNoMarkers(t, result, marker, "capturedV_🙂")
			} else {
				require.Contains(t, decodeErrorBody(t, result)["error"], marker)
			}
			require.Equal(t, beforeHTTP, e.upstream.requests.Load())
			require.Equal(t, beforeHandler, e.upstream.handlers.Load())
			next := e.call(t, "private", "check", "args", args, false)
			privacyAssertIndexedError(t, next, "private:check", change.after == config.ToolArgumentErrorModeRedacted)
			require.Equal(t, beforeHTTP, e.upstream.requests.Load())
			require.Equal(t, beforeHandler, e.upstream.handlers.Load())
			retry := e.call(t, "private", "check", "args", privacyValidArgs(), false)
			require.False(t, retry.IsError)
			require.Contains(t, fwdText(retry), "owned-loopback")
			require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
			require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
		})
	}
}

func TestArgumentPrivacyHTTPCapturedUpstreamPolicy(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	marker := "flightM_雪/~"
	for _, legacy := range []bool{false, true} {
		for _, change := range []struct{ before, after string }{
			{config.ToolArgumentErrorModeRedacted, config.ToolArgumentErrorModeDetailed},
			{"", config.ToolArgumentErrorModeRedacted},
			{config.ToolArgumentErrorModeDetailed, config.ToolArgumentErrorModeRedacted},
		} {
			t.Run(fmt.Sprintf("legacy=%t/%s_to_%s", legacy, change.before, change.after), func(t *testing.T) {
				sc, err := e.proxy.storage.GetUpstreamServer("private")
				require.NoError(t, err)
				sc.ToolArgumentErrorMode = change.before
				require.NoError(t, e.proxy.storage.SaveUpstreamServer(sc))
				changed := make(chan error, 1)
				hook := func() {
					updated := *sc
					updated.ToolArgumentErrorMode = change.after
					changed <- e.proxy.storage.SaveUpstreamServer(&updated)
				}
				e.upstream.beforeFail.Store(&hook)
				args := privacyValidArgs()
				args["text"] = marker
				beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
				result := e.call(t, "private", "reject", "args_json", args, legacy)
				e.upstream.beforeFail.Store(nil)
				select {
				case err := <-changed:
					require.NoError(t, err)
				default:
					t.Fatal("real upstream handler did not change the persisted policy")
				}
				persisted, err := e.proxy.storage.GetUpstreamServer("private")
				require.NoError(t, err)
				require.Equal(t, change.after, persisted.ToolArgumentErrorMode)
				privacyAssertUpstreamError(t, result, "private", marker, change.before == config.ToolArgumentErrorModeRedacted)
				require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
				require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
				next := e.call(t, "private", "reject", "args_json", args, legacy)
				privacyAssertUpstreamError(t, next, "private", marker, change.after == config.ToolArgumentErrorModeRedacted)
				require.Equal(t, beforeHTTP+2, e.upstream.requests.Load())
				require.Equal(t, beforeHandler+2, e.upstream.handlers.Load())
			})
		}
	}
}

func TestArgumentPrivacyHTTPAuthAndOuterWrapperControls(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	marker := "authM_雪/~"
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("real_auth/legacy=%t", legacy), func(t *testing.T) {
			args := privacyValidArgs()
			args["text"], args["kind"] = marker, "two"
			beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
			result := e.call(t, "private", "reject", "args", args, legacy)
			body := decodeErrorBody(t, result)
			require.Contains(t, body["error"], "401 Unauthorized")
			require.Contains(t, body["error"], marker)
			require.NotContains(t, body, "input_schema")
			require.NotContains(t, body, "hint")
			require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
			require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
		})
	}

	// Obtain the genuine trusted wrapper through the actual manager and SDK
	// HTTP path. An arbitrary outer wrapper must not grant cause selection.
	args := privacyValidArgs()
	args["text"] = marker
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, generic := e.proxy.upstreamManager.CallTool(ctx, "private:reject", args)
	require.Error(t, generic)
	cause, trusted := upstream.GenericToolCallCause(generic)
	require.True(t, trusted)
	require.Contains(t, cause.Error(), "validation failed for argument")
	for _, context := range []string{"outer request context", "authentication failed", "connection refused", "request timed out"} {
		t.Run(context, func(t *testing.T) {
			outer := fmt.Errorf("%s: %w", context, generic)
			_, trusted := upstream.GenericToolCallCause(outer)
			require.False(t, trusted)
			old := e.proxy.createDetailedErrorResponse(outer, "private", "reject")
			redacted := e.proxy.createToolErrorResponse(outer, "private", "reject", config.ToolArgumentErrorModeRedacted)
			require.Equal(t, old, redacted)
			require.Contains(t, decodeErrorBody(t, redacted)["error"], marker)
			require.NotContains(t, decodeErrorBody(t, redacted), "input_schema")
		})
	}
}

func TestArgumentPrivacyHTTPNonArgumentControls(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	for _, legacy := range []bool{false, true} {
		for _, control := range argumentPrivacyRPCControls {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, control.name), func(t *testing.T) {
				args := privacyValidArgs()
				args["text"] = "nonargM_雪/~"
				var previous map[string]interface{}
				for _, mode := range []string{"", config.ToolArgumentErrorModeDetailed, config.ToolArgumentErrorModeRedacted} {
					sc, err := e.proxy.storage.GetUpstreamServer("private")
					require.NoError(t, err)
					sc.ToolArgumentErrorMode = mode
					require.NoError(t, e.proxy.storage.SaveUpstreamServer(sc))
					beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
					body := decodeErrorBody(t, e.call(t, "private", control.name, "args", args, legacy))
					require.Len(t, body, 4)
					require.NotContains(t, body, "input_schema", "a known non-argument SDK identity must retain its classification")
					require.NotContains(t, body, "hint")
					require.Contains(t, body["error"], args["text"])
					if previous != nil {
						require.Equal(t, previous, body, "preserve the full default/detailed envelope and message")
					}
					previous = body
					require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
					require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
				}
			})
		}
	}
}
