package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f (F25, F26, F35): the profile evaluator behind effective-tools and
// the tool counts of a profile view.

func rowOf(t *testing.T, res *runtime.EffectiveToolsResult, server, tool string) runtime.EffectiveTool {
	t.Helper()
	for _, r := range res.Tools {
		if r.Server == server && r.Tool == tool {
			return r
		}
	}
	t.Fatalf("no row for %s:%s", server, tool)
	return runtime.EffectiveTool{}
}

func TestProfileV3EffectiveTools_AdminSeesEveryRowWithReasons(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	res, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{})
	require.NoError(t, err)
	assert.Equal(t, "work-readonly", res.Profile)

	assert.Equal(t, "above_tier_cap", rowOf(t, res, "github", "create_issue").Access.Reason)
	assert.Equal(t, "write", rowOf(t, res, "github", "create_issue").ProfileTier)
	assert.Equal(t, "server_not_in_profile", rowOf(t, res, "filesystem", "read_text_file").Access.Reason)
	assert.Equal(t, "denied_by_rule", rowOf(t, res, "github", "get_secret_scanning_alert").Access.Reason)
	assert.Equal(t, "unannotated_hidden", rowOf(t, res, "github", "search_code").Access.Reason)
	list := rowOf(t, res, "github", "list_issues")
	assert.True(t, list.Access.Visible)
	assert.True(t, list.Access.Callable)
	assert.Equal(t, "read", list.IntrinsicTier)
	assert.Equal(t, "unannotated", rowOf(t, res, "github", "search_code").IntrinsicTier)

	require.NotNil(t, res.Counts.Callable)
	assert.Equal(t, len(res.Tools), res.Counts.Visible+res.Counts.Hidden)
	assert.Equal(t, 2, res.Counts.ByReason["above_tier_cap"], "create_issue and delete_repo")
	assert.Empty(t, res.StaleClassifications)

	// Filters.
	only, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{Server: "notion"})
	require.NoError(t, err)
	require.Len(t, only.Tools, 1)
	byReason, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{Reason: "above_tier_cap"})
	require.NoError(t, err)
	for _, r := range byReason.Tools {
		assert.Equal(t, "above_tier_cap", r.Access.Reason)
	}
	require.NotEmpty(t, byReason.Tools)
}

func TestProfileV3EffectiveTools_ClassificationStaleIsIgnoredByDecide(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
		// Classify an ANNOTATED tool (list_issues is read) as destructive, and a
		// tool that does not exist: both are stale (FR-005).
		cfg.Profiles[0].Tools.Classify = map[string]string{"github:list_issues": "destructive", "github:gone_tool": "read"}
	})
	res, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{})
	require.NoError(t, err)
	row := rowOf(t, res, "github", "list_issues")
	assert.True(t, row.ClassificationStale)
	assert.Equal(t, row.IntrinsicTier, row.ProfileTier, "a classify entry never overrides an annotated tool")
	assert.True(t, row.Access.Callable)
	assert.Equal(t, []string{"github:gone_tool", "github:list_issues"}, res.StaleClassifications)
	assert.Equal(t, map[string]string{"github:gone_tool": "missing", "github:list_issues": "annotated"}, res.StaleClassificationReasons)
	for _, r := range res.Tools {
		if r.Tool != "list_issues" {
			assert.False(t, r.ClassificationStale, r.Tool)
		}
	}
}

func TestProfileV3EffectiveTools_NonAdminGetsVisibleRowsAndCountsOnly(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	viewer := runtime.ViewerScope{Restricted: true, Visible: func(server string) bool { return server != "notion" }, AllowedServers: []string{"github", "filesystem"}}
	res, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{Viewer: viewer})
	require.NoError(t, err)

	for _, r := range res.Tools {
		assert.True(t, r.Access.Visible, "%s:%s", r.Server, r.Tool)
		assert.NotEqual(t, "notion", r.Server, "a server the viewer cannot see never appears")
	}
	assert.Greater(t, res.Counts.Hidden, 0)
	assert.Nil(t, res.Counts.Callable, "callable is administrator-only")
	assert.Empty(t, res.Counts.ByReason)
	assert.Empty(t, res.StaleClassifications)

	body, err := json.Marshal(res)
	require.NoError(t, err)
	for _, leak := range []string{"above_tier_cap", "denied_by_rule", "server_not_in_profile", "unannotated_hidden", "create_issue", "delete_repo", "notion", "update_page"} {
		assert.NotContains(t, string(body), leak, "an excluded row, its tier or its reason must not be disclosed")
	}
}

func TestProfileV3EffectiveTools_ClientOnAnotherProfile(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)

	// Cursor as it is bound: create_issue is above the cap.
	bound, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{Client: "cursor"})
	require.NoError(t, err)
	assert.Equal(t, "above_tier_cap", rowOf(t, bound, "github", "create_issue").Access.Reason)

	// "What would Cursor get on work-full": the credential evaluated under that
	// profile, although its binding says work-readonly.
	onFull, err := f.proxy.EffectiveTools(context.Background(), "work-full", runtime.EffectiveToolsOptions{Client: "cursor"})
	require.NoError(t, err)
	assert.True(t, rowOf(t, onFull, "github", "create_issue").Access.Callable)

	_, err = f.proxy.EffectiveTools(context.Background(), "ghost", runtime.EffectiveToolsOptions{})
	require.ErrorIs(t, err, profile.ErrUnknownProfile)
	_, err = f.proxy.EffectiveTools(context.Background(), "work-full", runtime.EffectiveToolsOptions{Client: "no-such-client"})
	require.ErrorIs(t, err, profile.ErrUnknownClient)
}

func TestProfileV3ToolCounts_ByProfileTierAndMemoized(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	ctx := context.Background()
	counts := f.proxy.ToolCounts(ctx, "work-readonly", runtime.ViewerScope{})
	// Visible under work-readonly: github:list_issues (read), notion:update_page
	// (allowed write). The unannotated github:search_code is hidden.
	assert.Equal(t, 1, counts.Read)
	assert.Equal(t, 1, counts.Write)
	assert.Equal(t, 0, counts.Destructive)
	assert.Equal(t, 1, counts.UnannotatedHidden)

	again := f.proxy.ToolCounts(ctx, "work-readonly", runtime.ViewerScope{})
	assert.Equal(t, counts, again)
	idx := f.proxy.profileIndexCurrent(ctx)
	_, cached := f.proxy.toolCounts.get(idx, f.proxy.toolTierGeneration(), "work-readonly")
	assert.True(t, cached, "the administrator counts are memoized for the published snapshot")

	// A restricted viewer's counts cover only the servers it may see, and are
	// never served from (or written to) the administrator cache.
	scoped := f.proxy.ToolCounts(ctx, "work-readonly", runtime.ViewerScope{Restricted: true, Visible: func(s string) bool { return s == "github" }})
	assert.Equal(t, 1, scoped.Read)
	assert.Equal(t, 0, scoped.Write)

	full := f.proxy.ToolCounts(ctx, "work-full", runtime.ViewerScope{})
	assert.Greater(t, full.Read+full.Write+full.Destructive, counts.Read+counts.Write+counts.Destructive)
	assert.Equal(t, runtime.ToolCounts{}, f.proxy.ToolCounts(ctx, "ghost", runtime.ViewerScope{}))
}
