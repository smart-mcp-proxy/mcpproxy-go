package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-02 cross-review round 6.

func newUX02R6TrustedRuntime(t *testing.T, dir string) *Runtime {
	t.Helper()
	cfg := &config.Config{
		DataDir:  dir,
		Listen:   "127.0.0.1:0",
		Servers:  []*config.ServerConfig{{Name: "lib", Enabled: true}},
		Profiles: []config.ProfileConfig{{Name: "p", Servers: []string{"lib"}}},
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{Name: "lib", Enabled: true}))
	return rt
}

// Finding 1: a trusted server's discovery baseline is a baseline decision
// too. Replacing the whole inventory (A -> B) deletes A's approved record;
// B must not then be auto-approved as a "first trusted baseline" by a later
// pass or after a restart. Same with an authoritative empty refresh between.
func TestTrustedBaselineSurvivesInventoryReplacement(t *testing.T) {
	for _, variant := range []string{"direct-replace", "empty-refresh-between"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			rt := newUX02R6TrustedRuntime(t, dir)

			require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", ux02Tools("lib", 1)))
			require.Empty(t, ux02NotApproved(t, rt, "lib"), "the first inventory is the trusted baseline")
			require.NotNil(t, indexedTool(t, rt, "lib", "read_000"))

			if variant == "empty-refresh-between" {
				require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", []*config.ToolMetadata{}))
				recs, err := rt.storageManager.ListToolApprovals("lib")
				require.NoError(t, err)
				require.Empty(t, recs, "the empty refresh removes A's approval record")
			}

			replaced := []*config.ToolMetadata{ux02Destructive("lib")}
			for i := 0; i < 3; i++ {
				require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", replaced))
				require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "lib"), "pass %d", i)
				require.Nil(t, indexedTool(t, rt, "lib", "drop_all"), "pass %d: held tool must not be indexed", i)
			}
			rt.reindexAffectedProfiles("lib")
			require.Equal(t, uint64(0), profileDocCount(t, rt.IndexManager(), "p"), "held tool must not reach a profile index")

			require.NoError(t, rt.Close())
			rt = newUX02R6TrustedRuntime(t, dir)
			t.Cleanup(func() { _ = rt.Close() })
			require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", replaced))
			require.Equal(t, []string{"drop_all=pending"}, ux02NotApproved(t, rt, "lib"), "after restart")
			require.Nil(t, indexedTool(t, rt, "lib", "drop_all"), "after restart")
			res, err := rt.checkToolApprovals("lib", replaced)
			require.NoError(t, err)
			require.True(t, res.BlockedTools["drop_all"], "after restart the tool stays blocked")
		})
	}
}

// A trusted server whose first discovery is empty has not established a
// baseline yet: its first real inventory is still the trusted baseline.
func TestTrustedEmptyFirstDiscoveryIsNotABaseline(t *testing.T) {
	rt := newUX02R6TrustedRuntime(t, t.TempDir())
	t.Cleanup(func() { _ = rt.Close() })
	ctx := context.Background()
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", []*config.ToolMetadata{}))
	require.NoError(t, rt.applyDifferentialToolUpdate(ctx, "lib", ux02Tools("lib", 2)))
	require.Empty(t, ux02NotApproved(t, rt, "lib"))
}
