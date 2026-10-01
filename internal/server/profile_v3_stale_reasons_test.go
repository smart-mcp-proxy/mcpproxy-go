package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The reason a classify entry is stale comes from the server, which sees the
// UNFILTERED tool set: a --server or --reason filter must not change it.
func TestProfileV3EffectiveTools_StaleReasonsSurviveFilters(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
		cfg.Profiles[0].Tools.Classify = map[string]string{"github:list_issues": "destructive", "github:gone_tool": "read"}
	})
	want := map[string]string{
		"github:gone_tool":   profile.StaleClassificationMissing,
		"github:list_issues": profile.StaleClassificationAnnotated,
	}

	for name, opts := range map[string]runtime.EffectiveToolsOptions{
		"no filter":     {},
		"server filter": {Server: "notion"},
		"reason filter": {Reason: "above_tier_cap"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", opts)
			require.NoError(t, err)
			assert.Equal(t, want, res.StaleClassificationReasons)
			assert.Equal(t, []string{"github:gone_tool", "github:list_issues"}, res.StaleClassifications)
			if opts.Server == "notion" {
				for _, r := range res.Tools {
					assert.Equal(t, "notion", r.Server, "the filter still applies to the rows")
				}
			}
		})
	}

	t.Run("non-admin gets none", func(t *testing.T) {
		viewer := runtime.ViewerScope{Restricted: true, Visible: func(string) bool { return true }, AllowedServers: []string{"github"}}
		res, err := f.proxy.EffectiveTools(context.Background(), "work-readonly", runtime.EffectiveToolsOptions{Viewer: viewer})
		require.NoError(t, err)
		assert.Empty(t, res.StaleClassificationReasons)
		assert.Empty(t, res.StaleClassifications)
	})
}
