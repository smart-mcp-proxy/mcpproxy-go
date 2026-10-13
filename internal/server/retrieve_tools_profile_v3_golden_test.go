package server

import (
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// Spec 108 (Profiles v3) T021: the one new golden this PR adds
// (contracts/mcp-tools.md "Frozen goldens"). Legacy goldens
// (retrieve_full_default.golden.json etc.) are untouched (SC-003) — this
// file adds a NEW golden for the NEW non-legacy response shape only.
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
