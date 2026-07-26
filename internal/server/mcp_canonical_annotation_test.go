package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// TestCanonicalName_AnnotationsResolveOnLivePath is the #871 companion
// regression: once retrieve_tools identity is canonicalized to "server:tool",
// the full entry builder passes that PREFIXED name into the annotation lookup,
// while the live StateView stores tool names BARE. If the lookup did not
// normalize its input, annotations would silently drop and call_with would
// degrade back to call_tool_read (re-introducing Issue #306).
func TestCanonicalName_AnnotationsResolveOnLivePath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs full runtime + index + supervisor state view")
	}

	proxy, rt, _ := buildMCPProxyWithActivation(t)

	// Live path: StateView holds a BARE tool name, carrying a destructive hint.
	destructive := true
	rt.Supervisor().StateView().UpdateServer("github", func(s *stateview.ServerStatus) {
		s.Name = "github"
		s.Enabled = true
		s.Connected = true
		s.Tools = []stateview.ToolInfo{
			{
				Name:        "create_issue", // BARE, exactly as the live path stores it
				Description: "Create a GitHub issue",
				Annotations: &config.ToolAnnotations{DestructiveHint: &destructive},
			},
		}
	})

	t.Run("lookupToolAnnotations resolves a PREFIXED input against a bare StateView name", func(t *testing.T) {
		ann := proxy.lookupToolAnnotations("github", "github:create_issue")
		require.NotNil(t, ann, "annotations must resolve when the canonical (prefixed) name is passed")
		require.NotNil(t, ann.DestructiveHint)
		assert.True(t, *ann.DestructiveHint)
	})

	t.Run("full entry from a canonical SearchResult keeps annotations and call_with", func(t *testing.T) {
		result := &config.SearchResult{
			Tool: &config.ToolMetadata{
				// Canonical identity as the index read seams now return it.
				Name:       "github:create_issue",
				ServerName: "github",
			},
		}
		entry := proxy.buildFullToolEntry(result, toolEntryOpts{})
		assert.NotNil(t, entry["annotations"], "annotations must survive canonicalization")
		assert.Equal(t, contracts.ToolVariantDestructive, entry["call_with"],
			"call_with must not degrade to call_tool_read (#306 regression)")
	})
}
