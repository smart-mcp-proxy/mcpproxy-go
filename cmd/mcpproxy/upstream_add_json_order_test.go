package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// GH #2065: moving the add-json parser into configimport must not change which
// error add-json reports first. trust_mode was checked before the oauth block.
func TestParseAddJSONTrustModeCheckedBeforeOAuth(t *testing.T) {
	_, _, err := parseAddJSON("x", `{"url":"https://x/mcp","trust_mode":"bogus","oauth":{"redirect_uri":"https://example.com/cb"}}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --trust-mode")
}
