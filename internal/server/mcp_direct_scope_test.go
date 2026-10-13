package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestReadDirectToolStamp_RejectsForgedValue is the tool-side analogue of
// TestStripAggregatedPromptServer_PreservesUpstreamMeta's forged-stamp check
// in mcp_prompt_scope_test.go (PR #1326 review round 2, chunk D/E: the tool
// surface had no equivalent regression test). An upstream server that happens
// to send our own internal _meta key back — whether by coincidence or by a
// crafted response probing for a bypass — must never be trusted as a
// registration identity: only a value stampDirectTool itself produced (the
// unexported directToolStamp struct) can satisfy readDirectToolStamp, because
// a plain string or map under the same key does not type-assert to it.
func TestReadDirectToolStamp_RejectsForgedValue(t *testing.T) {
	cases := map[string]any{
		"bare string":        "github",
		"map mimicking it":   map[string]any{"owner": "github", "rawName": "list_repos"},
		"wrong-typed struct": struct{ Owner string }{Owner: "github"},
		"empty struct":       struct{}{},
	}

	for name, forgedValue := range cases {
		t.Run(name, func(t *testing.T) {
			forged := mcp.Tool{
				Name: "github__list_repos",
				Meta: &mcp.Meta{AdditionalFields: map[string]any{directToolStampMetaKey: forgedValue}},
			}

			stamp, ok := readDirectToolStamp(forged)
			assert.False(t, ok, "an upstream-supplied value under the stamp key is not a registration identity")
			assert.Equal(t, directToolStamp{}, stamp, "a rejected read must not leak a partially-populated stamp")

			// stripDirectToolStamp must not treat a forged value as a stamp to
			// strip either — the tool (and its forged _meta) passes through
			// untouched, exactly like an upstream tool with no stamp at all.
			assert.Equal(t, forged, stripDirectToolStamp(forged),
				"an unrecognized value under the stamp key must be left exactly as the upstream sent it")
		})
	}
}

// TestReadDirectToolStamp_AcceptsGenuineStamp is the positive control for the
// test above: stampDirectTool's own output must round-trip through
// readDirectToolStamp, so the forged-value rejection above is proven against
// a real stamp actually failing to round-trip, not against a helper that
// rejects everything.
func TestReadDirectToolStamp_AcceptsGenuineStamp(t *testing.T) {
	entry := &directCatalogEntry{ServerName: "github", ToolName: "list_repos", RequiredPermission: "read"}
	tool := mcp.Tool{Name: "github__list_repos"}

	stamped := stampDirectTool(tool, entry)
	stamp, ok := readDirectToolStamp(stamped)
	require.True(t, ok, "a tool this package itself stamped must be recognized")
	assert.Equal(t, "github", stamp.owner)
	assert.Equal(t, "list_repos", stamp.rawName)
	assert.Equal(t, "read", stamp.tier)
}

// Spec 105 PR G, FR-010 gap G6 (T098): at CALL TIME, a tool on an AUTHORIZED
// server that the caller is over its permission TIER for must reach the
// REGISTERED HANDLER — which answers insufficient-permission — rather than
// being excluded by the discovery filter chain into mcp-go's
// unregistered-name -32602.
//
// On HEAD (before this fix) the SAME filter that hides an out-of-scope tool
// at tools/list also excluded an over-tier tool at tools/call, so a
// read-only token calling a write tool on ITS OWN authorized server got the
// same "tool not found" envelope a genuinely out-of-scope probe gets —
// masking a real, callable-by-someone-with-the-right-tier tool as
// nonexistent, and losing the "Permission denied ... requires 'write'"
// signal. Zero upstream calls either way: the handler's own tier check
// (makeDirectModeHandler) runs before any dispatch code, so this is
// structurally guaranteed regardless of which envelope answers — what
// differs, and what this test is about, is the ENVELOPE: hidden stays
// -32602, over-tier-on-an-authorized-server must reach the handler.
func TestDirectModeHandler_OverTierOnAuthorizedServer_ReachesHandlerAtCallTime(t *testing.T) {
	tools := []*config.ToolMetadata{
		skewTool("a", "write_tool", "Writes something", `{"type":"object"}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false)}),
	}
	f := newSkewFixture(t, tools)

	readOnlyA := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "a-read-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead},
	})

	// withDirectRequestKindBox mirrors what mcpAuthMiddleware installs on
	// every real HTTP request before mcp-go's HandleMessage runs; a bare ctx
	// here — something production never actually hands to HandleMessage —
	// would fall back to the list-time default and re-exclude the tool,
	// proving nothing about the call-time behaviour this test targets.
	ctx := withDirectRequestKindBox(readOnlyA)

	initMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	require.NotNil(t, f.proxy.directServer.HandleMessage(ctx, initMsg))

	encoded, err := json.Marshal(f.proxy.directServer.HandleMessage(ctx,
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"a__write_tool","arguments":{}}}`)))
	require.NoError(t, err)

	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &envelope))

	// A protocol-level error means the caller never reached the handler at
	// all — exactly the disclosure gap this test guards against.
	if errObj, isErr := envelope["error"]; isErr && errObj != nil {
		t.Fatalf("an over-tier call on an authorized server must reach the handler, not be refused at the protocol level: %v", errObj)
	}
	require.NotNil(t, envelope["result"],
		"the request must succeed at the JSON-RPC level — the refusal lives inside the tool result: %v", envelope)

	result, ok := envelope["result"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, result["isError"], "the tool result itself must report an error")
	content, ok := result["content"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, content)
	block, ok := content[0].(map[string]interface{})
	require.True(t, ok)
	text, _ := block["text"].(string)
	assert.Contains(t, text, "Permission denied")
	assert.Contains(t, text, "'write'")
}

// directCallEnvelope drives one tools/call through HandleMessage and reports
// its shape: "protocol_error" (a JSON-RPC-level -32602), or "tool_error" /
// "tool_ok" for a result that reached the handler, plus the result text when
// there is one.
func directCallEnvelope(t *testing.T, f *skewFixture, ctx context.Context, displayName string) (shape, text string) {
	t.Helper()
	ctx = withDirectRequestKindBox(ctx)
	initMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	require.NotNil(t, f.proxy.directServer.HandleMessage(ctx, initMsg))

	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{}}}`, displayName)
	encoded, err := json.Marshal(f.proxy.directServer.HandleMessage(ctx, []byte(msg)))
	require.NoError(t, err)

	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &envelope))

	if errObj, isErr := envelope["error"]; isErr && errObj != nil {
		errMap, _ := errObj.(map[string]interface{})
		msgText, _ := errMap["message"].(string)
		return "protocol_error", msgText
	}
	result, ok := envelope["result"].(map[string]interface{})
	require.True(t, ok, "a non-error envelope must carry a result: %v", envelope)
	content, _ := result["content"].([]interface{})
	var resultText string
	if len(content) > 0 {
		if block, ok := content[0].(map[string]interface{}); ok {
			resultText, _ = block["text"].(string)
		}
	}
	if isErr, _ := result["isError"].(bool); isErr {
		return "tool_error", resultText
	}
	return "tool_ok", resultText
}

// Spec 105 PR G, FR-010 gap G6/T104a: the precedence regression through
// HandleMessage. Three cells, on the SAME authorized server "a", for a
// {read}-only token:
//   - a WITHIN-tier tool that is locked (pending approval) alone: -32602,
//     UNCHANGED from before this PR — this cell was never the disclosure gap.
//   - an OVER-tier tool that is otherwise callable: insufficient-permission,
//     reaching the handler (the gap T098 covers as a single case).
//   - an OVER-tier tool that is ALSO locked (pending): insufficient-
//     permission, TIER-FIRST (spec.md:133) — the precedence a two-filter
//     chain could get backwards if the callability filter did not also know
//     about the tier exception.
func TestDirectModeHandler_FR010Precedence_TierFirstOverLock(t *testing.T) {
	tools := []*config.ToolMetadata{
		skewTool("a", "locked_read_tool", "Locked but within tier", `{"type":"object"}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("a", "over_tier_tool", "Destructive, otherwise callable", `{"type":"object"}`,
			&config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
		skewTool("a", "locked_over_tier_tool", "Destructive AND locked", `{"type":"object"}`,
			&config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, tools)

	// Override two of the three approval records to PENDING (newSkewFixture
	// seeded all three APPROVED).
	for _, name := range []string{"locked_read_tool", "locked_over_tier_tool"} {
		require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: name, Status: storage.ToolApprovalStatusPending,
		}))
	}

	readOnlyA := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "a-read-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead},
	})

	shape, text := directCallEnvelope(t, f, readOnlyA, "a__locked_read_tool")
	assert.Equal(t, "protocol_error", shape, "within-tier + locked alone must stay the unregistered-name -32602: %s", text)

	shape, text = directCallEnvelope(t, f, readOnlyA, "a__over_tier_tool")
	assert.Equal(t, "tool_error", shape, "over-tier alone must reach the handler: %s", text)
	assert.Contains(t, text, "Permission denied")

	shape, text = directCallEnvelope(t, f, readOnlyA, "a__locked_over_tier_tool")
	assert.Equal(t, "tool_error", shape, "over-tier + locked must ALSO reach the handler (tier-first): %s", text)
	assert.Contains(t, text, "Permission denied", "the tier gate must win over the approval lock: %s", text)
	assert.NotContains(t, text, "pending", "a tier-first refusal must not also claim a pending-approval reason")
}

// codex round-2 review, MUST-FIX: filterDirectModeToolsForAuth (and every
// other direct-mode scope/tier predicate built on isScopeRestrictedCaller)
// used to compute "is this caller scope-restricted" as
// `authCtx.Type == auth.AuthTypeAgent` — which silently treated a
// server-edition "user" identity (auth.AuthTypeUser: a real scoped OAuth
// caller, not an administrator) as unrestricted, exactly like an admin. A
// user token scoped to server "a" alone must NOT see server "b"'s tools on
// the direct surface, the same way an agent token would not.
func TestFilterDirectModeToolsForAuth_UserTypeIsScopeRestricted(t *testing.T) {
	tools := []*config.ToolMetadata{
		skewTool("a", "read_a", "Read something on a", `{"type":"object"}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("b", "read_b", "Read something on b", `{"type":"object"}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, tools)

	userScopedToA := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeUser,
		AgentName:      "user-a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead},
	})

	listed := f.listed(userScopedToA)
	_, hasA := listed["a__read_a"]
	_, hasB := listed["b__read_b"]
	assert.True(t, hasA, "a user token scoped to 'a' must still see its own server's tools")
	assert.False(t, hasB, "a user token scoped to 'a' must NOT see server 'b' — the bug let it see every server")

	// describe_tool must agree with the listing (SC-007 parity).
	assert.True(t, f.describable(userScopedToA, "a__read_a"))
	assert.False(t, f.describable(userScopedToA, "b__read_b"),
		"describe_tool must refuse a tool this user token cannot list, exactly like an agent token")
}
