package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// Spec 108 (Profiles v3) T019: `/mcp/all` `tools/list` omits excluded tools
// for a caller with an effective profile — filterDirectModeToolsForAuth's
// STAMPED branch (the production path every real upstream tool takes; see
// mcp_direct_scope.go's own doc comment on the unstamped fallback being
// test-only) now also runs the FR-011 policy decision, LIST TIME ONLY.
func directStampedTool(server, rawName, tier string) mcp.Tool {
	entry := &directCatalogEntry{ServerName: server, ToolName: rawName, RequiredPermission: tier}
	return stampDirectTool(mcp.Tool{Name: server + "__" + rawName}, entry)
}

func TestFilterDirectModeToolsForAuth_ProfileV3(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)

	tools := []mcp.Tool{
		directStampedTool("github", "list_issues", "read"),
		directStampedTool("github", "create_issue", "write"),
		directStampedTool("notion", "update_page", "write"),
		directStampedTool("filesystem", "read_text_file", "read"),
	}

	t.Run("work-readonly: list-time filter excludes create_issue (tier cap) but keeps the allow-ruled notion:update_page", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "work-readonly")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "notion__update_page"}, filtered)
	})

	t.Run("work-full: everything in scope is admitted", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "work-full")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "github__create_issue", "notion__update_page", "filesystem__read_text_file"}, filtered)
	})

	t.Run("legacy: policy never excludes (only server scope does, which the fixture's legacy profile restricts to github)", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "legacy")
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, tools))
		assert.ElementsMatch(t, []string{"github__list_issues", "github__create_issue"}, filtered)
	})

	t.Run("call-time request: a policy-excluded tool must stay visible to the filter chain (108-d's own gate answers the call, not this filter)", func(t *testing.T) {
		ctx := withDirectRequestKindBox(urlProfileCtx(proxy, "work-readonly"))
		setDirectRequestKind(ctx, directRequestKindCall)
		filtered := filterDirectToolNames(proxy.filterDirectModeToolsForAuth(ctx, []mcp.Tool{directStampedTool("github", "create_issue", "write")}))
		assert.Equal(t, []string{"github__create_issue"}, filtered, "list-time exclusion must not leak into the call-time re-evaluation")
	})
}

func filterDirectToolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	return names
}
