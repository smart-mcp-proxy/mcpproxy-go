package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 085 direct-routing coverage. Direct mode renders its own error bodies
// (mcp_routing.go) instead of the call_tool_* paths, so the opt-in argument
// policy has to be honoured there too — and ONLY there: every non-argument
// failure must keep the exact text direct mode has always returned.

// directArgumentPrivacyEntry is the privacy fixture tool as a direct catalog
// entry. The display id is the direct surface's "server__tool" form.
func directArgumentPrivacyEntry() *directCatalogEntry {
	return &directCatalogEntry{
		DisplayName: "private__reject",
		ServerName:  "private",
		ToolName:    "reject",
		Description: "Independent privacy fixture",
		ParamsJSON:  argumentPrivacySchema,
		Hash:        "h-private-reject",
	}
}

// directArgumentPrivacyMetadata is the same tool as an upstream projection, so
// the catalog is rendered through the real path.
func directArgumentPrivacyMetadata() *config.ToolMetadata {
	e := directArgumentPrivacyEntry()
	return &config.ToolMetadata{
		ServerName:  e.ServerName,
		Name:        e.ToolName,
		Description: e.Description,
		ParamsJSON:  e.ParamsJSON,
		Hash:        e.Hash,
	}
}

// setDirectArgumentErrorMode persists one server's policy. Direct dispatch reads
// the stored record at call time, so this is the policy the next call captures.
func setDirectArgumentErrorMode(t *testing.T, p *MCPProxyServer, server, mode string) {
	t.Helper()
	sc, err := p.storage.GetUpstreamServer(server)
	require.NoError(t, err)
	sc.ToolArgumentErrorMode = mode
	require.NoError(t, p.storage.SaveUpstreamServer(sc))
}

// callRenderedDirect dispatches one call through the handler the real render
// path produced.
func callRenderedDirect(t *testing.T, handler mcpserver.ToolHandlerFunc, display string, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = display
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// resultText is the single text block a plain-text (non-JSON) direct error
// carries.
func directPlainText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "a plain direct error is text content")
	return text.Text
}

// DIR-LOCAL-01: a direct-mode call rejected by the stored-schema validator
// honours the server's policy. Redacted suppresses the rejected keys/values;
// default and detailed keep the specific diagnostic and are identical to each
// other.
func TestDirectArgumentPrivacyLocalSchema(t *testing.T) {
	const (
		marker = "badK_雪/~"
		value  = "badV_🙂"
	)
	cases := []struct {
		name string
		args map[string]interface{}
	}{
		{"missing_required", map[string]interface{}{"kind": "one"}},
		{"unknown_property", map[string]interface{}{"text": "safe", "kind": "one", marker: value}},
	}
	bodies := map[string][]map[string]interface{}{}
	schemas := map[string]string{}
	for _, tc := range []struct {
		name     string
		mode     string
		redacted bool
	}{
		{"redacted", config.ToolArgumentErrorModeRedacted, true},
		{"default", "", false},
		{"detailed", config.ToolArgumentErrorModeDetailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := createTestMCPProxyServer(t)
			require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{
				Name: "private", Enabled: true, ToolArgumentErrorMode: tc.mode,
			}))
			require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "private", ToolName: "reject", Status: storage.ToolApprovalStatusApproved,
			}))
			entry := directArgumentPrivacyEntry()
			handler, _ := renderedDirectHandler(t, p, directArgumentPrivacyMetadata())

			for _, call := range cases {
				result := callRenderedDirect(t, handler, entry.DisplayName, call.args)
				body := decodeErrorBody(t, result)
				schemas[tc.name] = string(mustJSON(t, body["input_schema"]))

				require.Equal(t, "invalid_params", body["error_type"])
				require.Equal(t, entry.DisplayName, body["tool"],
					"the local rejection names the id the caller used")
				require.Contains(t, body["hint"], "describe_tool")
				if tc.redacted {
					require.Len(t, body, 5, "preserve exactly the established indexed error fields")
					require.Equal(t, "Invalid tool arguments.", body["error"])
					privacyAssertNoMarkers(t, result, marker, value)
				} else {
					require.Contains(t, body["error"], "invalid arguments for "+entry.DisplayName+":")
					if call.name == "unknown_property" {
						require.Contains(t, body["error"], marker,
							"default/detailed retain the specific diagnostic")
					}
				}
				bodies[tc.name] = append(bodies[tc.name], body)
			}
		})
	}
	require.Equal(t, bodies["default"], bodies["detailed"],
		"the omitted and explicit detailed policies render identically")
	require.Len(t, bodies["redacted"], len(cases))
	require.JSONEq(t, argumentPrivacySchema, schemas["redacted"],
		"the full stored schema survives redaction")
	require.JSONEq(t, argumentPrivacySchema, schemas["default"])
}

// DIR-UPSTREAM-01: a direct-mode upstream argument error renders the shared
// self-healing envelope only under the explicit redacted policy. Default and
// detailed keep the pre-existing plain text byte for byte.
func TestDirectArgumentPrivacyUpstreamError(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	require.NoError(t, e.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "private", ToolName: "reject", Status: storage.ToolApprovalStatusApproved,
	}))
	meta := e.proxy.lookupIndexedTool("private", "reject")
	require.NotNil(t, meta)
	handler, _ := renderedDirectHandler(t, e.proxy, directArgumentPrivacyMetadata())
	entry := directArgumentPrivacyEntry()

	const marker = "rpcM_雪/~"
	args := privacyValidArgs()
	args["text"] = marker
	expectedPlain := fmt.Sprintf(
		"Error calling private:reject: tool 'reject' on server 'private' failed: CallTool failed for 'reject': invalid params: validation failed for argument 'text': %s at /%s. Check server configuration, authentication, and tool parameters",
		marker, marker)

	setDirectArgumentErrorMode(t, e.proxy, "private", config.ToolArgumentErrorModeRedacted)
	beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
	redactedResult := callRenderedDirect(t, handler, entry.DisplayName, args)
	body := decodeErrorBody(t, redactedResult)
	require.Len(t, body, 5, "the shared indexed envelope, not the plain direct text")
	require.Equal(t, "invalid_params", body["error_type"])
	require.Equal(t, "private:reject", body["tool"],
		"the shared renderer names the canonical upstream id")
	require.Equal(t, "Invalid tool arguments.", body["error"])
	require.Contains(t, body["hint"], "describe_tool")
	require.JSONEq(t, argumentPrivacySchema, string(mustJSON(t, body["input_schema"])))
	privacyAssertNoMarkers(t, redactedResult, marker)
	require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
	require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())

	var plain []string
	for _, mode := range []string{"", config.ToolArgumentErrorModeDetailed} {
		setDirectArgumentErrorMode(t, e.proxy, "private", mode)
		beforeHTTP, beforeHandler = e.upstream.requests.Load(), e.upstream.handlers.Load()
		result := callRenderedDirect(t, handler, entry.DisplayName, args)
		text := directPlainText(t, result)
		require.Equal(t, expectedPlain, text,
			"default/detailed keep the exact direct-mode text, wrapper included")
		require.NotContains(t, text, "input_schema")
		require.NotContains(t, text, "hint")
		plain = append(plain, text)
		require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
		require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
	}
	require.Equal(t, plain[0], plain[1], "the omitted and explicit detailed policies render identically")
}

// DIR-CONTROL-01: non-argument failures keep their exact direct-mode text under
// every policy. A broad rewrite of the direct error branch would change
// auth/transport/timeout/SDK-sentinel responses; this pins them.
func TestDirectArgumentPrivacyNonArgumentControls(t *testing.T) {
	e := newArgumentPrivacyEnv(t)
	const marker = "nonargM_雪/~"
	args := privacyValidArgs()
	args["text"] = marker

	names := make([]string, 0, len(argumentPrivacyRPCControls)+1)
	for _, control := range argumentPrivacyRPCControls {
		names = append(names, control.name)
	}
	names = append(names, "reject")

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, e.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "private", ToolName: name, Status: storage.ToolApprovalStatusApproved,
			}))
			meta := e.proxy.lookupIndexedTool("private", name)
			require.NotNil(t, meta)
			handler, _ := renderedDirectHandler(t, e.proxy, &config.ToolMetadata{
				ServerName: "private", Name: name, Description: "Independent privacy fixture",
				ParamsJSON: argumentPrivacySchema, Hash: "h-private-" + name,
			})
			display := FormatDirectToolName("private", name)

			callArgs := map[string]interface{}{"text": marker, "kind": "one"}
			if name == "reject" {
				// A genuine authentication branch, not the generic wrapper.
				callArgs["kind"] = "two"
			}
			var first string
			for _, mode := range []string{"", config.ToolArgumentErrorModeDetailed, config.ToolArgumentErrorModeRedacted} {
				setDirectArgumentErrorMode(t, e.proxy, "private", mode)
				beforeHTTP, beforeHandler := e.upstream.requests.Load(), e.upstream.handlers.Load()
				result := callRenderedDirect(t, handler, display, callArgs)
				text := directPlainText(t, result)
				if first == "" {
					first = text
				} else {
					require.Equal(t, first, text, "policy %q changed a non-argument response", mode)
				}
				require.Contains(t, text, "Error calling private:"+name,
					"the established direct failure text is preserved")
				require.NotContains(t, text, "input_schema")
				require.NotContains(t, text, "invalid_params")
				require.NotContains(t, text, "hint")
				require.Contains(t, text, marker, "non-argument diagnostics are not redacted")
				require.Equal(t, beforeHTTP+1, e.upstream.requests.Load())
				require.Equal(t, beforeHandler+1, e.upstream.handlers.Load())
			}
			if name == "reject" {
				require.Contains(t, first, "401 Unauthorized", "the fixture must exercise the auth branch")
			}
		})
	}

	t.Run("transport_without_client", func(t *testing.T) {
		p := newDirectValidationProxy(t)
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{
			Name: "fs", Enabled: true, ToolArgumentErrorMode: config.ToolArgumentErrorModeRedacted,
		}))
		result := callDirect(t, p, validationEntry(), map[string]interface{}{"path": "/tmp"})
		text := directPlainText(t, result)
		require.Contains(t, text, "Error calling fs:read")
		require.NotContains(t, text, "input_schema")
		require.NotContains(t, text, "invalid_params")
	})

	t.Run("canceled_context", func(t *testing.T) {
		require.NoError(t, e.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "private", ToolName: "reject", Status: storage.ToolApprovalStatusApproved,
		}))
		handler, _ := renderedDirectHandler(t, e.proxy, directArgumentPrivacyMetadata())
		var first string
		for _, mode := range []string{"", config.ToolArgumentErrorModeRedacted} {
			setDirectArgumentErrorMode(t, e.proxy, "private", mode)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := mcp.CallToolRequest{}
			req.Params.Name = directArgumentPrivacyEntry().DisplayName
			req.Params.Arguments = privacyValidArgs()
			result, err := handler(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, result)
			text := directPlainText(t, result)
			if first == "" {
				first = text
			} else {
				require.Equal(t, first, text, "a timeout/cancellation shape must not follow the policy")
			}
			require.NotContains(t, text, "input_schema")
			require.NotContains(t, text, "invalid_params")
		}
	})
}

// REST-PATCH-PROD-01: on the real controller the PATCH is resolved inside the
// commit (httpapi.ServerPatcher), so a parked PATCH that omits the policy must
// not re-apply the value it would have read before a newer write. The ported
// REST-02 test only covers the fallback-controller branch.
func TestArgumentErrorModeRESTPatchProduction(t *testing.T) {
	srv, cfgPath := newSpec116Server(t, tenantServer())
	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)
	require.Implements(t, (*httpapi.ServerPatcher)(nil), srv,
		"the production commit-time PATCH branch must be the one under test")

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	api.SetPatchAfterParseHookForTest(func(string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-release
		}
	})

	patchDone := make(chan int, 1)
	go func() { patchDone <- restPatch(t, api, "tenant", `{"args":["new"]}`).Code }()
	awaitSignal(t, entered, "the REST PATCH to park")

	// The newer write lands while the first PATCH is parked before its commit.
	newer := restPatch(t, api, "tenant", `{"tool_argument_error_mode":"redacted"}`)
	require.Equal(t, 200, newer.Code, newer.Body.String())

	close(release)
	require.Equal(t, 200, <-patchDone)

	for label, sc := range map[string]*config.ServerConfig{
		"live":    liveServer(srv, "tenant"),
		"storage": storedServer(t, srv, "tenant"),
		"disk":    diskServer(t, cfgPath, "tenant"),
	} {
		require.NotNil(t, sc, label)
		require.Equal(t, []string{"new"}, sc.Args, "%s: the parked PATCH's own field", label)
		require.Equal(t, config.ToolArgumentErrorModeRedacted, sc.ToolArgumentErrorMode,
			"%s: the newer policy survives an unrelated parked PATCH", label)
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
