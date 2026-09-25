package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109 FR-028 / T015 (X11): `mcpproxy tools list --tier`/`--risk` filter
// on the backend-computed `tier` field, and the table carries a TIER column
// — none of it derived locally.

// TestApplyGlobalToolFilters_TierX11Regression pins the bug this PR fixes:
// the filter used to inspect `annotations.operation_type`, a field that does
// not exist anywhere on the real GET /tools payload (operation_type is an
// intent-declaration concept, unrelated to a tool's MCP annotations). Over a
// realistic fixture — a tool whose annotations mark it read-only, the way the
// backend actually reports one — `--risk read` matched nothing.
func TestApplyGlobalToolFilters_TierX11Regression(t *testing.T) {
	realisticReadOnlyTool := []map[string]interface{}{
		{
			"name":        "list_repos",
			"server_name": "github",
			// This is what a real read-only tool's annotations look like —
			// no "operation_type" key anywhere, because that key belongs to
			// intent declarations, not MCP tool annotations.
			"annotations": map[string]interface{}{"readOnlyHint": true},
			// This is the field the backend actually computes and sends
			// (contracts.AnnotationTier) — what the filter must use.
			"tier": "read",
		},
	}

	got := applyGlobalToolFilters(realisticReadOnlyTool, "", "read", "")
	require.Len(t, got, 1, "X11: --risk/--tier must match the backend-computed tier field, not a nonexistent annotations.operation_type")
	assert.Equal(t, "list_repos", got[0]["name"])
}

func TestApplyGlobalToolFilters_TierAndRiskAreAliases(t *testing.T) {
	tools := []map[string]interface{}{
		{"name": "delete_file", "server_name": "fs", "tier": "destructive"},
		{"name": "write_file", "server_name": "fs", "tier": "write"},
		{"name": "read_file", "server_name": "fs", "tier": "read"},
		{"name": "mystery_tool", "server_name": "fs", "tier": "unannotated"},
	}

	for _, tc := range []struct {
		filter string
		want   string
	}{
		{"destructive", "delete_file"},
		{"write", "write_file"},
		{"read", "read_file"},
		{"unannotated", "mystery_tool"},
	} {
		got := applyGlobalToolFilters(tools, "", tc.filter, "")
		require.Len(t, got, 1, "tier=%s", tc.filter)
		assert.Equal(t, tc.want, got[0]["name"], "tier=%s", tc.filter)
	}
}

// TestResolvedTierFilter_RiskIsAnAliasOfTier proves --risk and --tier drive
// the same filter, with --tier taking priority if a caller somehow sets both.
func TestResolvedTierFilter_RiskIsAnAliasOfTier(t *testing.T) {
	origTier, origRisk := toolsTierFilter, toolsRiskFilter
	defer func() { toolsTierFilter, toolsRiskFilter = origTier, origRisk }()

	toolsTierFilter, toolsRiskFilter = "", ""
	assert.Equal(t, "", resolvedTierFilter())

	toolsTierFilter, toolsRiskFilter = "", "write"
	assert.Equal(t, "write", resolvedTierFilter(), "--risk alone must apply")

	toolsTierFilter, toolsRiskFilter = "read", ""
	assert.Equal(t, "read", resolvedTierFilter(), "--tier alone must apply")

	toolsTierFilter, toolsRiskFilter = "destructive", "read"
	assert.Equal(t, "destructive", resolvedTierFilter(), "--tier wins when both are set")
}

// TestOutputGlobalTools_TierColumn proves the TIER column exists and renders
// the backend-computed value verbatim.
func TestOutputGlobalTools_TierColumn(t *testing.T) {
	tools := []map[string]interface{}{
		{
			"name":            "delete_repo",
			"server_name":     "github",
			"description":     "Delete a repository",
			"approval_status": "approved",
			"tier":            "destructive",
			"usage":           float64(1),
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	setOutputGlobals(t, "table", false)
	err := outputGlobalTools(tools)

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	outStr := buf.String()

	require.NoError(t, err)
	assert.Contains(t, outStr, "TIER", "table must contain a TIER column")
	assert.Contains(t, outStr, "destructive", "TIER column must render the backend-computed tier verbatim")
}
