package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 085 US2 — describe_tool (contracts/describe_tool.md):
//   - T025 (FR-010): definition FIELD-equal to the full-mode retrieve_tools
//     entry over {name, description, inputSchema, server, annotations,
//     call_with}; no score key; mixed valid/unknown ⇒ per-id errors with the
//     batch succeeding; >5 ids ⇒ single limit error, nothing processed.
//   - T026 (FR-011, Constitution IV): id resolution goes through the SAME
//     visibility predicate retrieve_tools uses (p.toolVisibleToSession) —
//     an out-of-scope/quarantined/pending/changed/disabled id yields a per-id
//     error, never a definition.
//   - T028 (FR-011): registered in the retrieve_tools routing mode only
//     (default server + buildCallToolModeTools), absent from code_execution
//     and direct mode; definition ≤150 tokens under tiktoken cl100k_base.
//   - T029 (FR-012): byte-identical output under full and compact
//     tool_response_mode.

// describeToolResponse decodes the JSON the describe_tool handler returns.
type describeToolResponse struct {
	Definitions []map[string]interface{} `json:"definitions"`
	Errors      []map[string]interface{} `json:"errors"`
}

// callDescribeRaw invokes the describe_tool handler and returns the raw
// result (which may be an error result).
func callDescribeRaw(t *testing.T, proxy *MCPProxyServer, ctx context.Context, ids []interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": ids}
	result, err := proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// callDescribe invokes describe_tool and decodes the success response.
func callDescribe(t *testing.T, proxy *MCPProxyServer, ctx context.Context, ids []interface{}) describeToolResponse {
	t.Helper()
	result := callDescribeRaw(t, proxy, ctx, ids)
	require.False(t, result.IsError, "describe_tool returned an error result: %v", result.Content)
	text := result.Content[0].(mcp.TextContent).Text
	var resp describeToolResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	return resp
}

// fullModeEntryFor captures the full-mode retrieve_tools entry for one tool.
func fullModeEntryFor(t *testing.T, proxy *MCPProxyServer, query, name string) map[string]interface{} {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": query, "limit": float64(20), "detail": "full",
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)
	for _, entry := range resp.Tools {
		if entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("tool %s not in full-mode retrieve_tools results", name)
	return nil
}

// T025 (FR-010): a definition is field-equal to the full-mode retrieve_tools
// entry — the exact contract recipe: capture the full entry, delete its
// ranked-only "score" key, and the remainder must be byte-equal to the
// definition. The definition itself must never carry a score.
func TestDescribeTool_DefinitionFieldEqualToFullMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	fullEntry := fullModeEntryFor(t, proxy, "manage", "github:create_issue")
	require.Contains(t, fullEntry, "score", "full-mode entries carry the ranked score")
	delete(fullEntry, "score")

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"github:create_issue"})
	require.Len(t, resp.Definitions, 1)
	require.Empty(t, resp.Errors)
	def := resp.Definitions[0]

	assert.NotContains(t, def, "score", "describe_tool is a lookup, not a ranked search — no score key (FR-010)")

	// Field equality over the definition fields — asserted as whole-map
	// equality after the score strip, so the shared fields cannot drift.
	wantJSON, err := json.Marshal(fullEntry)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(def)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON),
		"definition must be field-equal to the full-mode entry over {name, description, inputSchema, server, annotations, call_with}")

	// Belt and braces: the named contract fields individually.
	for _, field := range []string{"name", "description", "inputSchema", "server", "call_with"} {
		assert.Equal(t, fullEntry[field], def[field], "definition field %q must match the full-mode entry", field)
	}
}

// T025 (FR-010): mixed valid + unknown ids ⇒ definitions for the valid ones,
// per-id errors for the rest; the call as a whole succeeds.
func TestDescribeTool_MixedValidAndUnknownIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{
		"github:create_issue",
		"github:no_such_tool",
		"not-a-valid-id",
	})

	require.Len(t, resp.Definitions, 1, "the one valid id must resolve")
	assert.Equal(t, "github:create_issue", resp.Definitions[0]["name"])

	require.Len(t, resp.Errors, 2, "each unresolvable id gets its own error entry")
	byID := map[string]map[string]interface{}{}
	for _, e := range resp.Errors {
		byID[e["id"].(string)] = e
	}
	require.Contains(t, byID, "github:no_such_tool")
	assert.Equal(t, "not_found", byID["github:no_such_tool"]["error"])
	// Spec 102 FR-009: the hint points at discovery WITHOUT naming
	// retrieve_tools, which the direct surface does not expose.
	assert.Equal(t, describeNotFoundRemediation, byID["github:no_such_tool"]["remediation"],
		"remediation must point the agent back at discovery")
	assert.NotContains(t, byID["github:no_such_tool"]["remediation"], "retrieve_tools",
		"the shared remediation must stay surface-neutral")
	require.Contains(t, byID, "not-a-valid-id")
	assert.Equal(t, "not_found", byID["not-a-valid-id"]["error"])
}

// T025 (FR-010): >5 ids ⇒ one limit error naming the cap; the batch is not
// processed (anti-bulk-loophole, spec edge case).
func TestDescribeTool_TooManyIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	result := callDescribeRaw(t, proxy, context.Background(), []interface{}{
		"github:create_issue", "github:list_issues", "github:get_repo",
		"weather:get_forecast", "weather:search_city", "github:create_issue",
	})
	require.True(t, result.IsError, "6 ids must be rejected outright")
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "too many tool_ids: 6 (max 5)")
	assert.NotContains(t, text, "definitions", "no partial dump on a limit error")
}

// T025: missing / empty tool_ids follow the existing param-error convention.
func TestDescribeTool_MissingOrEmptyIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := proxy.handleDescribeTool(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Missing required parameter 'tool_ids'")

	result = callDescribeRaw(t, proxy, context.Background(), []interface{}{})
	require.True(t, result.IsError, "an empty tool_ids list is a param error, not an empty batch")
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "tool_ids")
}

// T026 (FR-011, Constitution IV): describe_tool must never return a definition
// the same session's retrieve_tools could not return — asserted against the
// SAME predicate (p.toolVisibleToSession) on the same session, across the
// agent-scope, quarantine, pending/changed, and disabled fixture cases.
func TestDescribeTool_VisibilityParityWithRetrieve(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	// Agent token scoped to github+quarry (gitlab out of scope) — the exact
	// session the T009 retrieve-parity test drives.
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "parity-bot",
		AllowedServers: []string{"github", "quarry"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	cases := []struct {
		id        string
		wantError string // "" = definition expected
	}{
		{"github:visible_tool", ""},
		// Spec 099 FR-011: an out-of-scope id is plain not_found — the retired
		// `invisible` code confirmed that a tool this session may not see exists.
		{"gitlab:scoped_tool", "not_found"},
		{"quarry:lingering_tool", "quarantined"},
		{"github:pending_tool", "pending_approval"},
		{"github:changed_tool", "changed"},
		{"github:disabled_tool", "disabled"},
		{"github:ghost_tool", "not_found"},
	}

	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			resp := callDescribe(t, proxy, ctx, []interface{}{c.id})

			server, tool, ok := splitServerTool(c.id)
			require.True(t, ok)
			visible, _ := proxy.toolVisibleToSession(ctx, server, tool)

			if c.wantError == "" {
				require.True(t, visible, "fixture: %s must be visible to the predicate", c.id)
				require.Len(t, resp.Definitions, 1)
				assert.Equal(t, c.id, resp.Definitions[0]["name"])
				assert.Empty(t, resp.Errors)
			} else {
				require.False(t, visible, "fixture: %s must be invisible to the predicate", c.id)
				assert.Empty(t, resp.Definitions,
					"describe_tool returned a definition retrieve_tools could not (%s)", c.id)
				require.Len(t, resp.Errors, 1)
				assert.Equal(t, c.id, resp.Errors[0]["id"])
				assert.Equal(t, c.wantError, resp.Errors[0]["error"])
				assert.NotEmpty(t, resp.Errors[0]["remediation"])
				// Never leak withheld content: no schema/description fields on
				// error entries (a quarantined description is a TPA payload).
				assert.NotContains(t, resp.Errors[0], "inputSchema")
				assert.NotContains(t, resp.Errors[0], "description")
			}
		})
	}
}

// Whitespace is never significant in a canonical tool id: ids copied out of a
// transcript with stray padding must resolve to the same definition.
func TestDescribeTool_TrimsWhitespaceInIDs(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{
		"github:create_issue ",
		" github:create_issue",
		"github: create_issue",
	})

	assert.Empty(t, resp.Errors, "padded ids must resolve, not error")
	require.Len(t, resp.Definitions, 3)
	for _, def := range resp.Definitions {
		assert.Equal(t, "github:create_issue", def["name"],
			"the definition always carries the canonical id")
	}
}

// Server/tool ids are case-sensitive by design (approval, quarantine and
// agent-scope stores all key on exact case). A miscased id must therefore NOT
// resolve, but the remediation must name the canonical id instead of the
// generic not-found hint.
func TestDescribeTool_CaseMismatchDidYouMean(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"GITHUB:create_issue"})

	assert.Empty(t, resp.Definitions, "a miscased id must not silently resolve")
	require.Len(t, resp.Errors, 1)
	e := resp.Errors[0]
	assert.Equal(t, "GITHUB:create_issue", e["id"], "the error echoes the raw id the caller sent")
	assert.Equal(t, "not_found", e["error"])
	remediation, _ := e["remediation"].(string)
	assert.Contains(t, remediation, "case-sensitive")
	assert.Contains(t, remediation, "did you mean 'github:create_issue'")
}

// The same holds for a miscased tool segment.
func TestDescribeTool_ToolCaseMismatchDidYouMean(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"github:Create_Issue"})

	assert.Empty(t, resp.Definitions)
	require.Len(t, resp.Errors, 1)
	remediation, _ := resp.Errors[0]["remediation"].(string)
	assert.Contains(t, remediation, "did you mean 'github:create_issue'")
}

// Security invariant: a did-you-mean suggestion may never confirm the
// existence of a tool this session cannot see. Out-of-scope and quarantined
// ids keep the generic not-found remediation even when the only difference
// from a real id is case.
func TestDescribeTool_CaseMismatchNoScopeLeak(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "parity-bot",
		AllowedServers: []string{"github", "quarry"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	for _, c := range []struct{ id, leak string }{
		{"GITLAB:scoped_tool", "gitlab"},    // out of agent scope
		{"QUARRY:lingering_tool", "quarry"}, // server quarantined
	} {
		t.Run(c.id, func(t *testing.T) {
			resp := callDescribe(t, proxy, ctx, []interface{}{c.id})

			assert.Empty(t, resp.Definitions)
			require.Len(t, resp.Errors, 1)
			assert.Equal(t, "not_found", resp.Errors[0]["error"])
			remediation, _ := resp.Errors[0]["remediation"].(string)
			assert.Equal(t, describeNotFoundRemediation, remediation,
				"a suggestion would confirm that a tool the session cannot see exists")
			assert.NotContains(t, remediation, c.leak)
		})
	}
}

// A genuinely absent tool keeps the generic remediation — no invented
// suggestion.
func TestDescribeTool_GenuinelyMissingKeepsGenericRemediation(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"github:no_such_tool_at_all"})

	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "not_found", resp.Errors[0]["error"])
	remediation, _ := resp.Errors[0]["remediation"].(string)
	assert.Equal(t, describeNotFoundRemediation, remediation)
	assert.NotContains(t, remediation, "did you mean")
}

// T028 (FR-011): describe_tool is registered in the retrieve_tools routing
// mode only — the default server and buildCallToolModeTools — and absent from
// code_execution and direct mode.
func TestDescribeTool_RegisteredInRetrieveToolsModeOnly(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	t.Run("default server", func(t *testing.T) {
		st := proxy.server.GetTool("describe_tool")
		require.NotNil(t, st, "describe_tool must be registered on the default (retrieve_tools mode) server")
		assert.NotNil(t, proxy.server.GetTool("retrieve_tools"), "describe_tool sits beside retrieve_tools")
	})

	t.Run("call-tool routing mode", func(t *testing.T) {
		var found bool
		for _, st := range proxy.buildCallToolModeTools() {
			if st.Tool.Name == "describe_tool" {
				found = true
			}
		}
		assert.True(t, found, "describe_tool must be in buildCallToolModeTools")
	})

	t.Run("code-execution routing mode", func(t *testing.T) {
		for _, st := range proxy.buildCodeExecModeTools() {
			assert.NotEqual(t, "describe_tool", st.Tool.Name,
				"describe_tool must NOT be exposed in code_execution mode (v1)")
		}
	})

	t.Run("direct routing mode exposes describe_tool", func(t *testing.T) {
		// Spec 102 FR-009/FR-018 deliberately REVERSES the spec-085 v1 decision
		// asserted here before: deferral without a schema-recovery stage on the
		// same surface trades tokens for failed calls, so the Inspect step has to
		// live where the Catalog is.
		directTools, _ := proxy.buildDirectModeTools()
		found := false
		for _, st := range directTools {
			if st.Tool.Name == "describe_tool" {
				found = true
			}
		}
		assert.True(t, found, "describe_tool must be exposed in direct mode (spec 102 FR-009)")
	})
}

// T028 (spec 085 FR-011) / spec 099 FR-015: the describe_tool definition costs
// ≤describeToolTokenBudget tokens counted with tiktoken cl100k_base — the same
// pinned encoder the spec-083 profiler uses, so the budget and the profiler
// agree. The budget rose from 150 to 250 when check mode added two parameters;
// the exact bytes are additionally pinned by the tools/list goldens, so prose
// cannot drift silently under the ceiling.
func TestDescribeTool_DefinitionTokenBudget(t *testing.T) {
	tool := buildDescribeToolTool()
	serialized, err := json.Marshal(tool)
	require.NoError(t, err)

	enc, err := tiktoken.GetEncoding("cl100k_base")
	require.NoError(t, err, "cl100k_base encoding must be loadable (bench pins the same encoder)")

	tokens := len(enc.Encode(string(serialized), nil, nil))
	assert.LessOrEqual(t, tokens, describeToolTokenBudget,
		"describe_tool definition must stay within the %d-token budget (spec 099 FR-015); got %d tokens for %s",
		describeToolTokenBudget, tokens, serialized)

	// Schema sanity: one required array-of-strings param plus the two optional
	// check-mode params, and nothing else — the reserved expect_hashes is NOT
	// declared (FR-008).
	assert.Equal(t, "describe_tool", tool.Name)
	require.Contains(t, tool.InputSchema.Properties, "tool_ids")
	assert.Equal(t, []string{"tool_ids"}, tool.InputSchema.Required)
	idsSchema := tool.InputSchema.Properties["tool_ids"].(map[string]any)
	assert.Equal(t, "array", idsSchema["type"])

	require.Contains(t, tool.InputSchema.Properties, "check")
	assert.Equal(t, "boolean", tool.InputSchema.Properties["check"].(map[string]any)["type"])
	require.Contains(t, tool.InputSchema.Properties, "filters")
	filters := tool.InputSchema.Properties["filters"].(map[string]any)
	assert.Equal(t, "object", filters["type"])
	assert.Equal(t, map[string]any{
		"read_only_only":      map[string]any{"type": "boolean"},
		"exclude_destructive": map[string]any{"type": "boolean"},
		"exclude_open_world":  map[string]any{"type": "boolean"},
	}, filters["properties"], "the three spec-094 annotation filters, and only those (FR-007)")
	assert.NotContains(t, tool.InputSchema.Properties, describeCheckReservedHashes)
	assert.Len(t, tool.InputSchema.Properties, 3)
}

// T029 (FR-012): describe_tool output is byte-identical whether the configured
// tool_response_mode is full or compact — it ignores the mode entirely.
func TestDescribeTool_ModeIndependent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)
	ids := []interface{}{"github:create_issue", "weather:get_forecast", "github:no_such_tool"}

	proxy.config.ToolResponseMode = config.ToolResponseModeFull
	fullResult := callDescribeRaw(t, proxy, context.Background(), ids)
	require.False(t, fullResult.IsError)

	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	compactResult := callDescribeRaw(t, proxy, context.Background(), ids)
	require.False(t, compactResult.IsError)

	assert.Equal(t,
		fullResult.Content[0].(mcp.TextContent).Text,
		compactResult.Content[0].(mcp.TextContent).Text,
		"describe_tool must return identical bytes in both response modes (FR-012)")
}

// Spec 105 FR-009 (adversarial review, critique0 #3 / critique3 #3): the index
// resolver matches the canonical "<server>:<raw>" id ALONE. Its former
// bare-name alternate (`tool.Name == toolName`) could only ever match when a
// raw name equalled a sibling's canonical id — a raw "a:erase" on server "a"
// resolving to the "erase" document — which rendered one tool's schema under
// a foreign id and made describe claim existence for a name dispatch refuses.
// Round-trip: raw "a:erase" on server "a" is docID "a:a:erase" and reads back
// with RawName "a:erase"; describe "a:a:erase" resolves IT, describe "a:erase"
// resolves the plain "erase", and when only "erase" exists the raw "a:erase"
// resolves nothing.
func TestDescribeTool_SelfPrefixedRawName_ResolvesExactlyNeverAsSibling(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		ServerName: "a", Name: "erase", RawName: "erase", Description: "Plain erase.",
		ParamsJSON: `{"type":"object","properties":{"plain":{"type":"boolean"}}}`, Hash: "h-plain",
	}))

	t.Run("only erase indexed: raw a:erase resolves nothing", func(t *testing.T) {
		require.NotNil(t, proxy.lookupIndexedTool("a", "erase"))
		assert.Nil(t, proxy.lookupIndexedTool("a", "a:erase"),
			"a raw name equal to a sibling's canonical id must not resolve to that sibling")

		resp := callDescribe(t, proxy, context.Background(), []interface{}{"a:a:erase"})
		assert.Empty(t, resp.Definitions, "describe must not render erase's schema under a foreign id")
		require.Len(t, resp.Errors, 1)
	})

	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		ServerName: "a", Name: "a:erase", RawName: "a:erase", Description: "Self-prefixed erase.",
		ParamsJSON: `{"type":"object","properties":{"self":{"type":"boolean"}}}`, Hash: "h-self",
	}))

	t.Run("both indexed: each canonical id resolves its own document", func(t *testing.T) {
		self := proxy.lookupIndexedTool("a", "a:erase")
		require.NotNil(t, self, "raw a:erase must resolve once its own document exists")
		assert.Equal(t, "a:a:erase", self.Name)
		assert.Equal(t, "a:erase", self.RawName)
		assert.Equal(t, "h-self", self.Hash)

		plain := proxy.lookupIndexedTool("a", "erase")
		require.NotNil(t, plain)
		assert.Equal(t, "a:erase", plain.Name)
		assert.Equal(t, "h-plain", plain.Hash)

		resp := callDescribe(t, proxy, context.Background(), []interface{}{"a:a:erase", "a:erase"})
		require.Empty(t, resp.Errors)
		require.Len(t, resp.Definitions, 2)
		byName := map[string]map[string]interface{}{}
		for _, def := range resp.Definitions {
			byName[def["name"].(string)] = def
		}
		require.Contains(t, byName, "a:a:erase")
		require.Contains(t, byName, "a:erase")
		assert.Equal(t, "Self-prefixed erase.", byName["a:a:erase"]["description"])
		assert.Equal(t, "Plain erase.", byName["a:erase"]["description"])
	})
}

// Spec 105 FR-009 (migration review, parity finding 3): describe_tool must
// gate a self-prefixed raw name on ITS OWN record. The split id
// "a:a:erase" → (a, "a:erase") is the raw tool "a:erase" on server "a";
// re-normalizing that already-split pair stripped the server prefix a second
// time and gated the suffix sibling "erase" — so a pending "a:erase" rendered
// its definition on the approved sibling's gate (contract step 4 withholds
// pending definitions: a TPA exposure), and with the records reversed an
// approved "a:erase" was withheld on the pending sibling's gate.
func TestDescribeTool_SelfPrefixedRawName_GatedOnItsOwnRecord(t *testing.T) {
	seed := func(t *testing.T, eraseStatus, selfStatus string) *MCPProxyServer {
		t.Helper()
		proxy := createTestMCPProxyServer(t)
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
		require.True(t, proxy.currentConfig().IsQuarantineEnabled(), "fixture: the tool-level gate must be active")
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			ServerName: "a", Name: "erase", RawName: "erase", Description: "Plain erase.",
			ParamsJSON: `{"type":"object"}`, Hash: "h-plain",
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			ServerName: "a", Name: "a:erase", RawName: "a:erase", Description: "Self-prefixed erase.",
			ParamsJSON: `{"type":"object"}`, Hash: "h-self",
		}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: eraseStatus, ApprovedHash: "h", CurrentHash: "h",
		}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "a:erase", Status: selfStatus, ApprovedHash: "h", CurrentHash: "h",
		}))
		return proxy
	}

	t.Run("erase approved, a:erase pending: a:a:erase is withheld as pending", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalStatusApproved, storage.ToolApprovalStatusPending)
		visible, reason := proxy.toolVisibleToSession(context.Background(), "a", "a:erase")
		assert.False(t, visible, "the pending tool's definition must be withheld")
		assert.Equal(t, visReasonToolPendingApproval, reason, "gated on its own record, not the approved sibling's")

		resp := callDescribe(t, proxy, context.Background(), []interface{}{"a:a:erase", "a:erase"})
		require.Len(t, resp.Errors, 1)
		assert.Equal(t, "a:a:erase", resp.Errors[0]["id"])
		assert.Equal(t, describeErrPendingApproval, resp.Errors[0]["error"])
		require.Len(t, resp.Definitions, 1, "the approved sibling renders")
		assert.Equal(t, "a:erase", resp.Definitions[0]["name"])
		assert.Equal(t, "Plain erase.", resp.Definitions[0]["description"])
	})

	t.Run("erase pending, a:erase approved: a:a:erase renders", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalStatusPending, storage.ToolApprovalStatusApproved)
		visible, reason := proxy.toolVisibleToSession(context.Background(), "a", "a:erase")
		assert.True(t, visible, "an approved tool must not be withheld on the pending sibling's gate (reason %q)", reason)

		resp := callDescribe(t, proxy, context.Background(), []interface{}{"a:a:erase", "a:erase"})
		require.Len(t, resp.Errors, 1)
		assert.Equal(t, "a:erase", resp.Errors[0]["id"], "the pending sibling is the one withheld")
		assert.Equal(t, describeErrPendingApproval, resp.Errors[0]["error"])
		require.Len(t, resp.Definitions, 1)
		assert.Equal(t, "a:a:erase", resp.Definitions[0]["name"])
		assert.Equal(t, "Self-prefixed erase.", resp.Definitions[0]["description"])
	})

	t.Run("classifyServerToolStatus counts the raw name a:erase on its own record", func(t *testing.T) {
		proxy := seed(t, storage.ToolApprovalStatusApproved, storage.ToolApprovalStatusPending)
		assert.Equal(t, contracts.DisabledStatusPendingApproval, proxy.classifyServerToolStatus("a", "a:erase"))
		assert.Equal(t, contracts.DisabledToolStatus(""), proxy.classifyServerToolStatus("a", "erase"))
		counts := proxy.serverToolCounts("a", []string{"erase", "a:erase"})
		require.NotNil(t, counts)
		assert.Equal(t, 1, counts.Callable)
		assert.Equal(t, 1, counts.PendingApproval)
	})
}

// Spec 105 FR-009 (migration review, critique NIT): the implicit-pending
// record (a snapshot tool with NO stored record under an active gate) reaches
// describe_tool with the same no-record body dispatch answers
// (toolPendingApprovalResult): the approve-flow remediation would be a dead
// end because nothing is listed for review yet.
func TestDescribeTool_NoApprovalRecord_AnswersRediscoveryRemediation(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
	requireManualTrustGateActive(t, proxy, "a")
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		ServerName: "a", Name: "ns:erase", RawName: "ns:erase", Description: "Read ns:erase",
		ParamsJSON: `{"type":"object"}`, Hash: "h-ns",
	}))
	_, err := proxy.storage.GetToolApproval("a", "ns:erase")
	require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no record")

	gate := proxy.evaluateExactToolGate("a", "ns:erase")
	require.True(t, isImplicitPendingApproval(gate.approval), "fixture: the gate synthesizes the implicit-pending record")

	visible, reason := proxy.toolVisibleToSession(context.Background(), "a", "ns:erase")
	assert.False(t, visible)
	assert.Equal(t, visReasonToolNoApprovalRecord, reason)

	resp := callDescribe(t, proxy, context.Background(), []interface{}{"a:ns:erase"})
	assert.Empty(t, resp.Definitions, "the definition is withheld exactly like a stored pending record's")
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, describeErrPendingApproval, resp.Errors[0]["error"])
	remediation, _ := resp.Errors[0]["remediation"].(string)
	assert.Contains(t, remediation, "no approval record", "the no-record body, not the approve flow")
	assert.Contains(t, remediation, "upstream_servers operation=\"refresh\" name=\"a\"")
	assert.NotContains(t, remediation, "review and approve it in the mcpproxy UI", "the approve-flow dead end must not be offered")
}
