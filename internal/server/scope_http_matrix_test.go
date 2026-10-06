package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 105 PR H1, T111: the HTTP credential matrix (FR-014,
// contracts/differential-oracle.md "HTTP matrix"). Real agent tokens, minted
// through profile_integration_test.go's mintProfileAgentToken (T035 —
// generalised from mintPinnedToken; H1 reuses it, as plan.md's PR D row
// says it would), driving every fixed MCP surface over real loopback HTTP —
// this is the one test in the whole Spec 105 suite that goes through
// mcpAuthMiddleware end to end rather than an injected auth.AuthContext.
//
// FR-014's applicability matrix:
//
//	operation                    | retrieve (/mcp,/mcp/call) | direct (/mcp/all) | code (/mcp/code)
//	read_cache                   | yes                       | n/a                | n/a
//	set_profile                  | yes                       | n/a                | yes
//	retrieve_tools metadata      | yes                       | n/a                | yes (no detail)
//	prompts list/get             | yes                       | yes                | yes
//	tail_log / per-server ops    | yes                       | n/a                | yes
//	describe_tool                | yes                       | yes                | n/a
//	stored-script resolution     | yes (code exec enabled)   | n/a                | yes
//	execution tier                call_tool_*                 direct-name dispatch nested call_tool
//	publication filtering        | n/a                       | yes                | n/a
//
// `yes` cells are driven through the real HTTP surface with a scoped token
// and assert the same non-disclosure the in-process differential tests
// prove (no `E2E` in this file's test names — the CI skip regex would drop
// it, per contracts doc). `n/a` cells assert the tool name is simply
// unregistered on that surface (`-32602`), which is registration-level
// topology, not a scope decision, and is the SAME for every caller.

// httpMatrixEnv is a minimal real HTTP environment: two upstream servers,
// "allowed" (the scoped token's own) and "hidden" (outside its grant), each
// with one tool carrying a sentinel, wired through a real *Server with a
// live TCP listener.
type httpMatrixEnv struct {
	*profileTestEnv
	token      string            // scoped to "research-srv" only (profileTestEnv's own naming)
	sessionIDs map[string]string // per-PATH Mcp-Session-Id: each routing-mode endpoint is its own mcpserver.MCPServer with its own session namespace, so a session minted on /mcp is meaningless on /mcp/all
}

func newHTTPMatrixEnv(t *testing.T) *httpMatrixEnv {
	t.Helper()
	env := newProfileTestEnv(t)
	token := mintProfileAgentToken(t, env, "http-matrix-agent",
		[]string{"research-srv"}, []string{"read"}, "")
	return &httpMatrixEnv{profileTestEnv: env, token: token, sessionIDs: map[string]string{}}
}

// rpc POSTs one JSON-RPC request to path (relative to the base URL, e.g.
// "/mcp/all") with the scoped token's bearer credential and returns the
// decoded envelope plus the HTTP status code.
func (e *httpMatrixEnv) rpc(t *testing.T, path, method string, params map[string]interface{}) (map[string]interface{}, int) {
	t.Helper()
	body := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, e.baseURL+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+e.token)
	if sid := e.sessionIDs[path]; sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		e.sessionIDs[path] = sid
	}

	var envelope map[string]interface{}
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	}
	return envelope, resp.StatusCode
}

// initializeOn sends the initialize handshake on path with the scoped
// token, required before most surfaces accept a second call on the same
// connection-less HTTP JSON-RPC transport used here.
func (e *httpMatrixEnv) initializeOn(t *testing.T, path string) {
	t.Helper()
	_, status := e.rpc(t, path, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "scope-http-matrix", "version": "1.0"},
	})
	require.Equal(t, http.StatusOK, status, "initialize must succeed on %s for a validly scoped token", path)
}

// callTool sends one tools/call for name on path and returns its envelope.
func (e *httpMatrixEnv) callTool(t *testing.T, path, name string) map[string]interface{} {
	t.Helper()
	envelope, status := e.rpc(t, path, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": map[string]interface{}{},
	})
	require.Equal(t, http.StatusOK, status)
	return envelope
}

// unregisteredToolIsNA asserts the "n/a" cell shape: calling a tool name
// this surface never registers at all is refused with the JSON-RPC-LEVEL
// `-32602` "tool ... not found" mcp-go emits for any unregistered name —
// topology, identical for every caller kind, never a scope decision (never
// the application-level `isError:true` a REGISTERED tool's handler can also
// return for bad arguments, which would make this check pass vacuously for
// a tool that exists but was called wrong — cross-model review round 1
// caught exactly that gap in an earlier version of this function). Proven
// by comparing against a name guaranteed not to exist on ANY surface: the
// two must produce the SAME error code and an equivalent message shape.
func (e *httpMatrixEnv) unregisteredToolIsNA(t *testing.T, path, toolName string) {
	t.Helper()
	e.initializeOn(t, path)

	envelope := e.callTool(t, path, toolName)
	errObj, hasError := envelope["error"].(map[string]interface{})
	require.True(t, hasError, "n/a cell %s on %s: expected a JSON-RPC-level error (not an application-level result), got %v", toolName, path, envelope)
	code, _ := errObj["code"].(float64)
	assert.Equal(t, float64(-32602), code, "n/a cell %s on %s: expected the mcp-go unregistered-tool code -32602, got %v (%v)", toolName, path, code, errObj)
	msg := fmt.Sprint(errObj["message"])
	assert.True(t, strings.Contains(msg, "not found") || strings.Contains(msg, "unknown tool"),
		"n/a cell %s on %s: expected an unregistered-tool refusal, got %q", toolName, path, msg)

	// Positive control: a name that provably does not exist anywhere gets
	// the SAME code and an equivalent message — proving the n/a cell is
	// registration-level topology, not a disguised scope refusal that
	// happens to share wording.
	nonexistentEnvelope := e.callTool(t, path, "definitely-does-not-exist-anywhere-"+toolName)
	nonexistentErr, ok := nonexistentEnvelope["error"].(map[string]interface{})
	require.True(t, ok, "control: a definitely-nonexistent tool must also be a JSON-RPC error on %s, got %v", path, nonexistentEnvelope)
	nonexistentCode, _ := nonexistentErr["code"].(float64)
	assert.Equal(t, nonexistentCode, code, "n/a cell %s on %s: must carry the SAME error code as a definitely-nonexistent tool", toolName, path)
}

// TestScopeHTTPMatrix_RetrieveSurfaceHasReadCacheSetProfileTailLog is the
// `yes` column for the retrieve surface (/mcp, /mcp/call): read_cache,
// set_profile and tail_log are all registered and reachable by a scoped
// token over real HTTP.
func TestScopeHTTPMatrix_RetrieveSurfaceHasReadCacheSetProfileTailLog(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	for _, path := range []string{"/mcp", "/mcp/call"} {
		t.Run(path, func(t *testing.T) {
			env.initializeOn(t, path)
			for _, tool := range []string{"read_cache", "set_profile", "upstream_servers", "retrieve_tools"} {
				envelope, status := env.rpc(t, path, "tools/call", map[string]interface{}{
					"name":      tool,
					"arguments": minimalArgsFor(tool),
				})
				require.Equal(t, http.StatusOK, status, "%s on %s must be a registered tool (HTTP 200, JSON-RPC envelope)", tool, path)
				_, hasError := envelope["error"]
				assert.False(t, hasError, "%s on %s: expected the tool to be REGISTERED (an application-level refusal inside `result` is fine; a JSON-RPC method-level error is not) — envelope: %v", tool, path, envelope)
			}
		})
	}
}

// TestScopeHTTPMatrix_DirectSurfacePublicationFiltering is the `yes` cell for
// direct-surface (/mcp/all) publication filtering: a scoped token's
// tools/list on /mcp/all must list ONLY tools its own grant authorizes —
// the hidden server's tool, and its sentinel, must never appear on the wire.
func TestScopeHTTPMatrix_DirectSurfacePublicationFiltering(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	env.initializeOn(t, "/mcp/all")
	envelope, status := env.rpc(t, "/mcp/all", "tools/list", map[string]interface{}{})
	require.Equal(t, http.StatusOK, status)
	result, ok := envelope["result"].(map[string]interface{})
	require.True(t, ok, "tools/list must return a result: %v", envelope)
	tools, _ := result["tools"].([]interface{})
	require.NotEmpty(t, tools, "the scoped token must see its own research-srv tools")
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		assert.False(t, strings.Contains(name, "deploy"),
			"a research-srv-only token must never see deploy-srv's tools on the direct surface: %s", name)
		assert.False(t, strings.HasPrefix(name, "deploy_"), name)
	}
}

// TestScopeHTTPMatrix_CodeSurfaceHasSetProfileAndRetrieveNoDetail is the
// `yes` column for the code surface (/mcp/code): set_profile and
// retrieve_tools are registered there too (retrieve_tools without the
// `detail` override, per the applicability matrix note).
func TestScopeHTTPMatrix_CodeSurfaceHasSetProfileAndRetrieve(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	env.initializeOn(t, "/mcp/code")
	for _, tool := range []string{"set_profile", "retrieve_tools"} {
		envelope, status := env.rpc(t, "/mcp/code", "tools/call", map[string]interface{}{
			"name":      tool,
			"arguments": minimalArgsFor(tool),
		})
		require.Equal(t, http.StatusOK, status, "%s on /mcp/code must be registered", tool)
		_, hasError := envelope["error"]
		assert.False(t, hasError, "%s on /mcp/code: expected the tool to be REGISTERED — envelope: %v", tool, envelope)
	}
}

// TestScopeHTTPMatrix_NACellsAreUnregisteredNotScopeRefusals drives the
// applicability matrix's `n/a` cells: read_cache and tail_log
// (upstream_servers) do not exist on /mcp/all at all — calling them is the
// SAME unregistered-tool shape a nonexistent name gets, never a
// scope-specific refusal (which would itself be a disclosure that the tool
// exists elsewhere).
func TestScopeHTTPMatrix_NACellsAreUnregisteredNotScopeRefusals(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	env.unregisteredToolIsNA(t, "/mcp/all", "read_cache")
	env.unregisteredToolIsNA(t, "/mcp/all", "upstream_servers")
	env.unregisteredToolIsNA(t, "/mcp/all", "set_profile")
}

// TestScopeHTTPMatrix_ProfileURLSurfaceUniformRefusal is FR-014's
// `/mcp/p/<slug>` row: a scoped, UNPINNED token reaching a profile URL whose
// server set does not intersect its own grant gets the SAME uniform
// non-disclosing refusal a nonexistent slug gets — driven over real HTTP,
// re-proving TestProfile_ScopedUnpinnedRefusalUniform's invariant through
// this file's own token-minting path (mintProfileAgentToken rather than
// mintPinnedToken) as the HTTP-matrix contract asks for.
func TestScopeHTTPMatrix_ProfileURLSurfaceUniformRefusal(t *testing.T) {
	env := newHTTPMatrixEnv(t) // token allowed=[research-srv], unpinned
	disjoint, disjointStatus := env.rpc(t, "/mcp/p/deploy", "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]interface{}{"name": "t", "version": "1"},
	})
	nonexistent, nonexistentStatus := env.rpc(t, "/mcp/p/does-not-exist", "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]interface{}{"name": "t", "version": "1"},
	})
	require.Equal(t, http.StatusNotFound, disjointStatus)
	require.Equal(t, http.StatusNotFound, nonexistentStatus)
	assert.Equal(t, nonexistent["error"], disjoint["error"],
		"a non-selectable profile ('deploy', disjoint from this token's grant) must be indistinguishable from a nonexistent slug")

	// Positive control: the token's own selectable profile URL is reachable
	// (route matches, not the uniform refusal).
	own, ownStatus := env.rpc(t, "/mcp/p/research", "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]interface{}{"name": "t", "version": "1"},
	})
	assert.Equal(t, http.StatusOK, ownStatus, "the token's own selectable profile URL must be reachable: %v", own)
}

// TestScopeHTTPMatrix_PromptsListGetAcrossAllThreeSurfaces is FR-014's
// "prompts list/get" row — the one row the matrix marks `yes` on EVERY
// surface. Cross-model review round 2: the matrix wasn't covering this row
// at all.
func TestScopeHTTPMatrix_PromptsListGetAcrossAllThreeSurfaces(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	for _, path := range []string{"/mcp", "/mcp/all", "/mcp/code"} {
		t.Run(path, func(t *testing.T) {
			env.initializeOn(t, path)
			envelope, status := env.rpc(t, path, "prompts/list", map[string]interface{}{})
			require.Equal(t, http.StatusOK, status, "prompts/list on %s must be registered (HTTP 200)", path)
			_, hasError := envelope["error"]
			assert.False(t, hasError, "prompts/list on %s: expected REGISTERED (method-level error means unregistered) — envelope: %v", path, envelope)

			// prompts/get on a definitely-nonexistent name must be a plain
			// application refusal reachable through the registered method
			// (not a method-level -32601 "method not found" — that would
			// mean prompts/get itself is unregistered on this surface).
			getEnvelope, getStatus := env.rpc(t, path, "prompts/get", map[string]interface{}{"name": "definitely-nonexistent-prompt-xyz"})
			require.Equal(t, http.StatusOK, getStatus)
			if errObj, ok := getEnvelope["error"].(map[string]interface{}); ok {
				code, _ := errObj["code"].(float64)
				assert.NotEqual(t, float64(-32601), code, "prompts/get on %s: -32601 would mean the METHOD itself is unregistered on this surface, not just the prompt name", path)
			}
		})
	}
}

// TestScopeHTTPMatrix_DescribeToolRetrieveAndDirectCodeNA is FR-014's
// "describe_tool" row: yes on retrieve and direct, n/a on code.
func TestScopeHTTPMatrix_DescribeToolRetrieveAndDirectCodeNA(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	for _, path := range []string{"/mcp", "/mcp/all"} {
		t.Run(path+"_yes", func(t *testing.T) {
			env.initializeOn(t, path)
			envelope, status := env.rpc(t, path, "tools/call", map[string]interface{}{
				"name":      "describe_tool",
				"arguments": map[string]interface{}{"ids": []string{"research-srv:search_papers"}},
			})
			require.Equal(t, http.StatusOK, status)
			_, hasError := envelope["error"]
			assert.False(t, hasError, "describe_tool on %s: expected REGISTERED — envelope: %v", path, envelope)
		})
	}
	t.Run("/mcp/code_na", func(t *testing.T) {
		env.unregisteredToolIsNA(t, "/mcp/code", "describe_tool")
	})
}

// TestScopeHTTPMatrix_TailLogOperationRetrieveAndCode is FR-014's
// "tail_log / per-server ops" row driven with the EXPLICIT tail_log
// operation (not just upstream_servers' default `list`), on the two
// surfaces the matrix marks `yes`.
func TestScopeHTTPMatrix_TailLogOperationRetrieveAndCode(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	for _, path := range []string{"/mcp", "/mcp/code"} {
		t.Run(path, func(t *testing.T) {
			env.initializeOn(t, path)
			envelope, status := env.rpc(t, path, "tools/call", map[string]interface{}{
				"name":      "upstream_servers",
				"arguments": map[string]interface{}{"operation": "tail_log", "name": "research-srv", "lines": float64(10)},
			})
			require.Equal(t, http.StatusOK, status, "tail_log on %s must be registered", path)
			_, hasError := envelope["error"]
			assert.False(t, hasError, "tail_log on %s: expected REGISTERED — envelope: %v", path, envelope)
		})
	}
}

// TestScopeHTTPMatrix_ExecutionTierRetrieveCallToolVariants is FR-014's
// "execution tier" row on the retrieve surface: call_tool_read/write/destructive
// are all registered and reachable by a scoped token over real HTTP (the
// in-process differential tests, scope_target_tier_matrix_test.go's 54-cell
// table, prove the actual tier-enforcement logic in depth; this file's job
// is proving the HTTP/session path to reach them at all).
func TestScopeHTTPMatrix_ExecutionTierRetrieveCallToolVariants(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	env.initializeOn(t, "/mcp")
	for _, variant := range []string{"call_tool_read", "call_tool_write", "call_tool_destructive"} {
		t.Run(variant, func(t *testing.T) {
			envelope, status := env.rpc(t, "/mcp", "tools/call", map[string]interface{}{
				"name":      variant,
				"arguments": map[string]interface{}{"name": "research-srv:search_papers", "args": map[string]interface{}{}},
			})
			require.Equal(t, http.StatusOK, status, "%s must be registered", variant)
			_, hasError := envelope["error"]
			assert.False(t, hasError, "%s: expected REGISTERED (an application-level insufficient-permission refusal inside `result` is fine and expected for write/destructive here) — envelope: %v", variant, envelope)
		})
	}
}

// TestScopeHTTPMatrix_RetrieveToolsNonDisclosureOverRealHTTP is the
// content-level non-disclosure proof cross-model review rounds 2 and 3
// asked for: driven through mcpAuthMiddleware over real loopback HTTP (not
// an injected auth.AuthContext), a scoped token's retrieve_tools response
// must never contain hidden deploy-srv's tool names anywhere in the RAW
// wire text — a raw substring check, not merely a structural JSON field
// check, so a leak into an unexpected field (a suggestion, a diagnostic, an
// error message) would still be caught.
func TestScopeHTTPMatrix_RetrieveToolsNonDisclosureOverRealHTTP(t *testing.T) {
	env := newHTTPMatrixEnv(t)
	env.initializeOn(t, "/mcp")
	// The query text itself is echoed back verbatim in debug/query_analysis
	// fields (harmless — echoing the CALLER's own input is not a
	// disclosure), so the leak-check needles below are deliberately chosen
	// to be tool/server IDENTIFIERS that do not appear in the query text,
	// never the query words themselves.
	envelope, status := env.rpc(t, "/mcp", "tools/call", map[string]interface{}{
		"name":      "retrieve_tools",
		"arguments": map[string]interface{}{"query": "deployment rollback process", "limit": float64(20), "include_stats": true, "debug": true},
	})
	require.Equal(t, http.StatusOK, status)
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	rawText := string(raw)

	for _, needle := range []string{"deploy_app", "deploy-srv"} {
		assert.NotContains(t, rawText, needle,
			"a research-srv-only token's retrieve_tools response must never mention hidden deploy-srv's %q anywhere in the wire envelope, even though the query is deliberately chosen to match deploy-srv's rollback tool's description (\"Rollback deployment\")", needle)
	}

	// Positive control: an unrelated query that DOES match the token's own
	// authorized content proves real results flow through this same HTTP
	// path (the deploy-matching query above may legitimately return zero
	// hits once scope-filtered, which would make the absence check above
	// vacuous on its own).
	controlEnvelope, controlStatus := env.rpc(t, "/mcp", "tools/call", map[string]interface{}{
		"name":      "retrieve_tools",
		"arguments": map[string]interface{}{"query": "search academic papers", "limit": float64(20)},
	})
	require.Equal(t, http.StatusOK, controlStatus)
	controlResult, ok := controlEnvelope["result"].(map[string]interface{})
	require.True(t, ok, "control query must succeed: %v", controlEnvelope)
	controlRaw, err := json.Marshal(controlResult)
	require.NoError(t, err)
	assert.Contains(t, string(controlRaw), "search_papers",
		"control: the token's OWN authorized tool must be findable through this exact HTTP path, or the absence checks above prove nothing")
}

// minimalArgsFor returns just-enough arguments for a tool call to reach its
// handler (not necessarily to succeed) — this file only asserts REGISTRATION
// (the tool exists, JSON-RPC accepted it), not the application-level
// success/failure the in-process differential tests already cover in depth.
func minimalArgsFor(tool string) map[string]interface{} {
	switch tool {
	case "read_cache":
		return map[string]interface{}{"key": strings.Repeat("0", 64)}
	case "set_profile":
		return map[string]interface{}{}
	case "upstream_servers":
		return map[string]interface{}{"operation": "list"}
	case "retrieve_tools":
		return map[string]interface{}{"query": "test", "limit": float64(5)}
	case "code_execution":
		return map[string]interface{}{"code": "return 1;"}
	default:
		return map[string]interface{}{}
	}
}
