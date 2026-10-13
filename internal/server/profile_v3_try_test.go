package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108-f T074a (F28): POST /profiles/try evaluates a DRAFT profile against a
// query exactly as retrieve_tools would - filtering by policy BEFORE the limit -
// and reports what the draft hides. Nothing is persisted.

func newTryFixture(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
		cfg.Profiles = []config.ProfileConfig{{Name: "existing", Servers: []string{"a"}}}
	})
	startCountingUpstream(t, proxy, rt, "a",
		toolSpec{Name: "keep", Description: "widget status check", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		toolSpec{Name: "many", Description: "widget widget widget widget widget widget", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}},
	)
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:keep", ServerName: "a", Description: "widget status check", ParamsJSON: "{}",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "a:many", ServerName: "a", Description: "widget widget widget widget widget widget", ParamsJSON: "{}",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
	}))
	return proxy
}

func TestProfileV3Try_FiltersBeforeTheLimitAndListsWhatItHides(t *testing.T) {
	proxy := newTryFixture(t)
	res, err := proxy.TryProfile(context.Background(), config.ProfileConfig{Servers: []string{"a"}, MaxTier: config.ProfileTierRead}, "widget", 1)
	require.NoError(t, err)

	require.Len(t, res.Results, 1, "an excluded high-ranked hit must not consume the only result slot")
	tool, _ := res.Results[0]["tool"].(map[string]interface{})
	require.NotNil(t, tool)
	assert.Equal(t, "a:keep", tool["name"])
	assert.GreaterOrEqual(t, res.HiddenByProfile, 1)
	require.NotEmpty(t, res.Hidden)
	assert.Equal(t, "a", res.Hidden[0].Server)
	assert.Equal(t, "many", res.Hidden[0].Tool)
	assert.Equal(t, "above_tier_cap", res.Hidden[0].Reason)
	assert.False(t, res.HiddenTruncated)
}

func TestProfileV3Try_ADraftReplacesTheSameNamedProfileWithoutPersisting(t *testing.T) {
	proxy := newTryFixture(t)
	before := proxy.currentConfig()

	// "existing" has no cap: both tools are admitted. The draft under the same
	// name caps it at read and hides the write tool - without touching config.
	open, err := proxy.TryProfile(context.Background(), config.ProfileConfig{Name: "existing", Servers: []string{"a"}}, "widget", 10)
	require.NoError(t, err)
	assert.Len(t, open.Results, 2)
	assert.Empty(t, open.Hidden)

	capped, err := proxy.TryProfile(context.Background(), config.ProfileConfig{Name: "existing", Servers: []string{"a"}, MaxTier: "read"}, "widget", 10)
	require.NoError(t, err)
	assert.Len(t, capped.Results, 1)
	assert.Len(t, capped.Hidden, 1)

	assert.Same(t, before, proxy.currentConfig(), "no snapshot was published")
	for _, p := range proxy.currentConfig().Profiles {
		assert.Empty(t, p.MaxTier, "the stored profile is untouched")
	}
}

func TestProfileV3Try_HiddenListIsCapped(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
	})
	specs := make([]toolSpec, 0, 130)
	for i := 0; i < 130; i++ {
		specs = append(specs, toolSpec{Name: fmt.Sprintf("w%03d", i), Description: "widget writer", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}})
	}
	startCountingUpstream(t, proxy, rt, "a", specs...)
	for i := 0; i < 130; i++ {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: fmt.Sprintf("a:w%03d", i), ServerName: "a", Description: "widget writer", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
		}))
	}
	res, err := proxy.TryProfile(context.Background(), config.ProfileConfig{Servers: []string{"a"}, MaxTier: "read"}, "widget", 5)
	require.NoError(t, err)
	assert.Empty(t, res.Results)
	assert.Len(t, res.Hidden, hiddenListCap)
	assert.True(t, res.HiddenTruncated)
	assert.Greater(t, res.HiddenByProfile, hiddenListCap)
}
