package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// assertJSONFieldIsEmptyArray decodes raw and asserts field is exactly [] on
// the wire — the strict-client contract from issue #953 (null crashes clients
// that iterate the array).
func assertJSONFieldIsEmptyArray(t *testing.T, raw, field string) {
	t.Helper()
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	require.Contains(t, payload, field)
	require.JSONEq(t, `[]`, string(payload[field]),
		"%q must serialize as an empty array, not %s", field, payload[field])
}

// Issue #953: a retrieve_tools search with zero matches must serialize the
// tools array as [] — never null. Strict MCP clients iterate over the array
// and crash on null (e.g. Python's `'NoneType' object is not iterable`).
func TestRetrieveTools_EmptyResultSerializesEmptyArray(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	resp, raw := callRetrieve(t, proxy, map[string]interface{}{
		"query": "zzz-no-such-tool-anywhere", "limit": float64(10),
	})

	require.Equal(t, 0, resp.Total, "fixture must not match the nonsense query")
	assertJSONFieldIsEmptyArray(t, raw, "tools")
}

// Same contract for quarantine_security list_quarantined with no quarantined
// servers: "servers" must be [] on the wire, never null.
func TestListQuarantined_EmptySerializesEmptyArray(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result, err := proxy.handleListQuarantinedUpstreams(context.Background())
	require.NoError(t, err)
	require.False(t, result.IsError)

	raw := result.Content[0].(mcp.TextContent).Text
	assertJSONFieldIsEmptyArray(t, raw, "servers")
}
