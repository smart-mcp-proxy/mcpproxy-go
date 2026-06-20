package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveToolApprovalTargets_ServerTool verifies that positional
// <server>:<tool> args are parsed and grouped per server.
func TestResolveToolApprovalTargets_ServerTool(t *testing.T) {
	groups, allMode, err := resolveToolApprovalTargets(
		[]string{"github:create_issue", "github:list_repos", "gitlab:merge"},
		"", false)
	require.NoError(t, err)
	assert.False(t, allMode)
	assert.Len(t, groups, 2)
	assert.ElementsMatch(t, []string{"create_issue", "list_repos"}, groups["github"])
	assert.ElementsMatch(t, []string{"merge"}, groups["gitlab"])
}

// TestResolveToolApprovalTargets_BareToolsWithServer verifies that bare tool
// names are scoped to the --server flag.
func TestResolveToolApprovalTargets_BareToolsWithServer(t *testing.T) {
	groups, allMode, err := resolveToolApprovalTargets(
		[]string{"create_issue", "list_repos"}, "github", false)
	require.NoError(t, err)
	assert.False(t, allMode)
	assert.Len(t, groups, 1)
	assert.ElementsMatch(t, []string{"create_issue", "list_repos"}, groups["github"])
}

// TestResolveToolApprovalTargets_AllRequiresServer verifies that --all without
// --server is rejected.
func TestResolveToolApprovalTargets_AllRequiresServer(t *testing.T) {
	_, _, err := resolveToolApprovalTargets(nil, "", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--server")
}

// TestResolveToolApprovalTargets_All verifies that --all --server yields a
// single server entry in all-mode with an empty tool list.
func TestResolveToolApprovalTargets_All(t *testing.T) {
	groups, allMode, err := resolveToolApprovalTargets(nil, "github", true)
	require.NoError(t, err)
	assert.True(t, allMode)
	assert.Len(t, groups, 1)
	assert.Empty(t, groups["github"])
}

// TestResolveToolApprovalTargets_AllRejectsPositional verifies that mixing
// positional targets with --all is rejected.
func TestResolveToolApprovalTargets_AllRejectsPositional(t *testing.T) {
	_, _, err := resolveToolApprovalTargets([]string{"github:create_issue"}, "github", true)
	require.Error(t, err)
}

// TestResolveToolApprovalTargets_NoTargets verifies that an empty invocation
// (no args, no --all) is rejected with guidance.
func TestResolveToolApprovalTargets_NoTargets(t *testing.T) {
	_, _, err := resolveToolApprovalTargets(nil, "", false)
	require.Error(t, err)
}

// TestResolveToolApprovalTargets_BareToolNoServer verifies that a bare tool
// name without --server (and no colon) is rejected.
func TestResolveToolApprovalTargets_BareToolNoServer(t *testing.T) {
	_, _, err := resolveToolApprovalTargets([]string{"create_issue"}, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create_issue")
}

// TestResolveToolApprovalTargets_MixedColonAndServerFlag verifies that explicit
// server:tool args take precedence over the --server flag.
func TestResolveToolApprovalTargets_MixedColonAndServerFlag(t *testing.T) {
	groups, _, err := resolveToolApprovalTargets(
		[]string{"gitlab:merge", "bare_tool"}, "github", false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"merge"}, groups["gitlab"])
	assert.ElementsMatch(t, []string{"bare_tool"}, groups["github"])
}
