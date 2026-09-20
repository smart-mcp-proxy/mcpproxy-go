package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 PR F — FR008-G6: a server literally named "__a".
//
// FR-008 is explicit that this MUST be authorized normally, not withheld for
// being unparseable: "server `__a`'s tool has a recorded owner and tier and
// is authorized normally." Every one of its display names — "__a__review",
// "__a__admin_purge" — fails ParseDirectToolName (the "__" separator sits at
// index 0), which is exactly the shape that used to be misclassified as a
// built-in by structural inference. The catalog decides first, so a real
// registration identity is never denied merely because its name looks like
// that; only a name with NO identity at all (an empty raw tool name, FR008-G7)
// is withheld.

func underscoreServerTools() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		skewTool("__a", "review", "Review a change", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("__a", "admin_purge", "Purge everything", `{"type":"object"}`, &config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
	}
}

// TestDirectUnderscoreServer_SteadyState covers listed/describable/dispatchable
// for both an unrestricted agent and an administrator, and withheld +
// -32602 parity for an "a"-only token (which has no relation to "__a" at
// all — it must never be confused with it by any string-matching heuristic).
func TestDirectUnderscoreServer_SteadyState(t *testing.T) {
	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			f := newSkewFixtureInMode(t, underscoreServerTools(), mode)

			wildcard := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "wildcard",
				AllowedServers: []string{"*"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})
			admin := auth.WithAuthContext(context.Background(), auth.AdminContext())
			aOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "a-only",
				AllowedServers: []string{"a"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})

			for name, ctx := range map[string]context.Context{"wildcard-scoped agent": wildcard, "administrator": admin} {
				require.Containsf(t, f.listed(ctx), "__a__review", "%s: a server named __a must be listed normally, never withheld for its shape", name)
				require.Truef(t, f.describable(ctx, "__a__review"), "%s", name)

				entry, ok := f.proxy.resolveDirectDescribeID(ctx, "__a__review")
				require.Truef(t, ok, "%s", name)
				assert.Equalf(t, "__a", entry.ServerName, "%s: identity resolves to its real owner, not a parse artefact", name)

				// Dispatchable: the call reaches PAST every auth/scope/callability
				// gate. There is no real upstream connection behind this fixture's
				// catalog-only entry (DiscoverTools is what the skew fixture fakes,
				// per its own doc comment), so the call still fails — but with an
				// UPSTREAM dispatch error, never an auth refusal.
				result, err := f.registeredHandler(t, "__a__review")(ctx, mcp.CallToolRequest{
					Params: mcp.CallToolParams{Name: "__a__review"},
				})
				require.NoErrorf(t, err, "%s", name)
				if result.IsError {
					text := result.Content[0].(mcp.TextContent).Text
					assert.NotContainsf(t, text, "not found", "%s: must not be refused as unregistered", name)
					assert.NotContainsf(t, text, "Permission denied", "%s: must not be refused for tier", name)
					assert.NotContainsf(t, text, "not callable", "%s: must not be refused for callability", name)
				}
			}

			assert.NotContainsf(t, f.listed(aOnly), "__a__review", "an a-only token has no relation to __a and must not see it")
			assert.Falsef(t, f.describable(aOnly, "__a__review"), "a-only")

			result, err := f.registeredHandler(t, "__a__review")(aOnly, mcp.CallToolRequest{
				Params: mcp.CallToolParams{Name: "__a__review"},
			})
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Equal(t, "tool '__a__review' not found", result.Content[0].(mcp.TextContent).Text,
				"withheld with the same non-disclosing wording an unregistered name gets, never naming __a (D12)")
		})
	}
}

// TestDirectUnderscoreServer_SeamVariant proves the __a shape survives a
// publication seam exactly like any other server: an added __a tool must
// still be authorized by its real identity, not treated as ambiguous merely
// because it cannot be re-parsed.
func TestDirectUnderscoreServer_SeamVariant(t *testing.T) {
	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			base := []*config.ToolMetadata{
				skewTool("fs", "read", "Read a file", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
			}
			f := newSkewFixtureInMode(t, base, mode)

			wildcard := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "wildcard",
				AllowedServers: []string{"*"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})
			aOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "a-only",
				AllowedServers: []string{"a"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})

			added := append(base, skewTool("__a", "review", "Review a change", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}))
			require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "__a", Enabled: true}))
			require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "__a", ToolName: "review", Status: storage.ToolApprovalStatusApproved,
			}))

			f.rebuildPaused(t, added, func() {
				assert.Contains(t, f.listed(wildcard), "__a__review",
					"the newly-registered __a tool is stamped with its real identity immediately, mid-seam")
				assert.NotContains(t, f.listed(aOnly), "__a__review",
					"and an a-only token, unrelated to __a, never sees it")
			})

			assert.Contains(t, f.listed(wildcard), "__a__review")
			assert.NotContains(t, f.listed(aOnly), "__a__review")
		})
	}
}
