package server

import (
	"context"
	"encoding/json"
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
	callErr := callEnvelope["error"].(map[string]interface{})
	assert.Equal(t, float64(mcp.INVALID_PARAMS), callErr["code"])
	assert.Contains(t, callErr["message"], "not found")
}
