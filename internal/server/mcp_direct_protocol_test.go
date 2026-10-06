package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 PR F — T086a: protocol-level proof of two mechanisms the rest of
// this PR's design relies on (plan.md D7):
//
//  1. The internal directToolStamp never reaches the wire, for admin OR
//     agent callers, on tools/list.
//  2. mcp-go v1.0.0 RE-EVALUATES the tool filter chain at tools/call time
//     (passesToolFilters), not only at tools/list time — so a tool that is
//     REGISTERED but HIDDEN by the scope filter is refused with the
//     unregistered-name envelope (-32602) if a client calls it anyway,
//     without needing any call-time code of our own.
func TestDirectProtocol_StampNeverOnWire_FilterReEvaluatedAtCallTime(t *testing.T) {
	tools := []*config.ToolMetadata{
		skewTool("a", "read", "Read something", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("b", "read", "Read something else", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, tools)

	aOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())

	initMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)

	for name, ctx := range map[string]context.Context{"a-only agent": aOnly, "administrator": admin} {
		require.NotNil(t, f.proxy.directServer.HandleMessage(ctx, initMsg))

		encoded, err := json.Marshal(f.proxy.directServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)))
		require.NoError(t, err)

		var envelope struct {
			Result struct {
				Tools []json.RawMessage `json:"tools"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal(encoded, &envelope))
		require.NotEmpty(t, envelope.Result.Tools, "%s", name)

		for _, raw := range envelope.Result.Tools {
			// _meta itself may legitimately be present (a response hook stamps
			// "anthropic/maxResultSizeChars" AFTER the filter chain runs) — the
			// assertion is about OUR internal key specifically, which the
			// terminal stripDirectToolStampFilter must remove before any tool
			// reaches the wire, for every caller.
			assert.NotContainsf(t, string(raw), directToolStampMetaKey, "%s: the internal stamp must never reach the wire: %s", name, raw)
		}
	}

	// (b) call-time re-evaluation: "b__read" is REGISTERED (it exists in
	// s.tools) but HIDDEN from a-only by the scope filter. Calling it anyway
	// must be refused with the SAME envelope an unregistered name gets,
	// proving mcp-go re-ran the filter at call time rather than trusting
	// whatever tools/list happened to return earlier.
	require.NotNil(t, f.proxy.directServer.HandleMessage(aOnly, initMsg))
	callEncoded, err := json.Marshal(f.proxy.directServer.HandleMessage(aOnly,
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"b__read","arguments":{}}}`)))
	require.NoError(t, err)

	var callEnvelope map[string]interface{}
	require.NoError(t, json.Unmarshal(callEncoded, &callEnvelope))
	require.NotNil(t, callEnvelope["error"], "a hidden REGISTERED tool must still be refused at call time: %v", callEnvelope)
	require.Nil(t, callEnvelope["result"], "a refusal must never carry a result alongside the error")
	callErr := callEnvelope["error"].(map[string]interface{})
	assert.Equal(t, float64(mcp.INVALID_PARAMS), callErr["code"])
	assert.Contains(t, callErr["message"], "not found")

	// PR #1326 review round 2, chunk C/finding #3: full envelope equality, not
	// just error code + substring. A hidden-but-registered tool's refusal must
	// be BYTE-IDENTICAL in shape and wording to what mcp-go answers for a name
	// that was never registered at all — the whole point of D12 is that a
	// caller cannot distinguish "authorized-but-blocked" from "genuinely
	// doesn't exist".
	unregisteredEncoded, err := json.Marshal(f.proxy.directServer.HandleMessage(aOnly,
		[]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"totally__unregistered","arguments":{}}}`)))
	require.NoError(t, err)

	var unregisteredEnvelope map[string]interface{}
	require.NoError(t, json.Unmarshal(unregisteredEncoded, &unregisteredEnvelope))
	require.NotNil(t, unregisteredEnvelope["error"], "a genuinely unregistered name must be refused too: %v", unregisteredEnvelope)
	require.Nil(t, unregisteredEnvelope["result"])
	unregisteredErr := unregisteredEnvelope["error"].(map[string]interface{})

	// Only jsonrpc/id/error may appear in either envelope — id legitimately
	// differs (3 vs 4, the request's own id echoed back), so it is excluded
	// from the equality check below rather than asserted equal.
	assert.ElementsMatchf(t, mapKeysForTest(callEnvelope), mapKeysForTest(unregisteredEnvelope),
		"the top-level envelope shape (jsonrpc/id/error, no result) must match exactly")
	assert.ElementsMatchf(t, mapKeysForTest(callErr), mapKeysForTest(unregisteredErr),
		"the error object's own field set (code/message, no extra data) must match exactly")

	assert.Equal(t, unregisteredErr["code"], callErr["code"],
		"a hidden-but-registered tool's refusal code must be byte-identical to a genuinely unregistered name's")
	// The message text is identical once the caller-supplied name is
	// substituted back in — that substitution is the ONLY difference D12
	// permits (the caller-supplied name may be echoed), never a distinct
	// scope-reason phrase, code, or extra field.
	wantMessage := strings.Replace(unregisteredErr["message"].(string), "totally__unregistered", "b__read", 1)
	assert.Equal(t, wantMessage, callErr["message"],
		"a hidden-but-registered tool's refusal text must be byte-identical to a genuinely unregistered name's, with only the echoed name differing")
}

// mapKeysForTest returns m's top-level keys, for an order-independent
// envelope-shape comparison via assert.ElementsMatch.
func mapKeysForTest(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
