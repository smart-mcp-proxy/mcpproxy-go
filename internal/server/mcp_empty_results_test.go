package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.NotContains(t, raw, `"tools":null`, "nil slice must not serialize as null")
	assert.True(t, strings.Contains(raw, `"tools":[]`),
		"empty result must serialize tools as an empty array, got: %s", raw)
}
