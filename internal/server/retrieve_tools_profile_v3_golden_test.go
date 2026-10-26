package server

import (
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// Spec 108 (Profiles v3) T021: profile-surface golden for the new response
// shape (contracts/mcp-tools.md "Frozen goldens"). This golden is refreshed
// alongside the other retrieve_tools response goldens when the compact
// routing guidance changes.
// Regenerate with: UPDATE_GOLDEN=1 go test ./internal/server -run
// TestRetrieveToolsProfileV3_GoldenByteIdentity
func TestRetrieveToolsProfileV3_GoldenByteIdentity(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "list_issues", "limit": float64(10)}
	result, err := proxy.handleRetrieveTools(urlProfileCtx(proxy, "work-readonly"), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error result")
	got := resultText(t, result)

	compareGolden(t, filepath.Join("testdata", "retrieve_tools_profile_v3.golden.json"), got)
}
