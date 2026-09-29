package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// seedCachePagingFixture adds three MORE read-tier github tools sharing a
// distinctive description term, on top of newProfilesV3Fixture's own five —
// retrieve_tools' truncator needs a multi-element array to build a real
// read_cache page from; a single-tool response falls back to SimpleTruncate
// (no pagination handle at all, mcp_retrieve_tools_truncation_test.go's own
// fixture makes the same choice for the same reason). They ride the SAME
// already-certified discovery epoch startCountingUpstream stamped for
// "github", so identity resolution (EffectiveAnnotations/isExactToolCallable)
// treats them exactly like the original five.
func seedCachePagingFixture(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime) {
	t.Helper()
	extra := []stateview.ToolInfo{
		{Name: "cache_item_a", Description: "cachepagetest", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "cache_item_b", Description: "cachepagetest", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "cache_item_c", Description: "cachepagetest", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
	}
	rt.Supervisor().StateView().UpdateServer("github", func(s *stateview.ServerStatus) {
		s.Tools = append(append([]stateview.ToolInfo{}, s.Tools...), extra...)
		s.ToolCount = len(s.Tools)
	})
	for _, tool := range extra {
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "github:" + tool.Name, ServerName: "github", Description: tool.Description, ParamsJSON: "{}",
			Annotations: tool.Annotations,
		}))
	}
	// The per-profile physical index is a snapshot COPY (Manager.
	// RebuildProfileFromShared), taken once by indexEnforcementMatrixFixtureTools
	// before these three docs existed — it must be rebuilt again now that the
	// shared index holds them too, or a profiled search finds nothing.
	require.NoError(t, proxy.index.RebuildProfileFromShared("work-readonly", []string{"github", "notion"}))
}

// Spec 108 (Profiles v3) T020/T024 (FR-027): a read_cache page cached under
// one policy must be refused once that policy changes, even when the
// server-scope dimension (ProfileServers, already covered pre-108) is
// unchanged — a profile fingerprint edit, or an out-of-band tool
// re-annotation with no config change at all.
func TestCacheAuthz_ProfileV3_PolicyFingerprintAndToolTierGeneration(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	seedCachePagingFixture(t, proxy, rt)

	pinned := pinnedProfileCtx("work-readonly")

	readCacheReq := func(key string) *mcp.CallToolResult {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"key": key, "offset": float64(0), "limit": float64(1)}
		result, err := proxy.handleReadCache(pinned, req)
		require.NoError(t, err)
		return result
	}

	retrieveArgs := map[string]interface{}{"query": "cachepagetest", "limit": float64(10)}
	retrieveReq := func() mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = retrieveArgs
		return req
	}

	// Baseline (no truncation) to size the fixture, exactly as
	// mcp_retrieve_tools_truncation_test.go does.
	fullResult, err := proxy.handleRetrieveTools(pinned, retrieveReq())
	require.NoError(t, err)
	full := resultText(t, fullResult)
	limit := len(full) / 2
	require.Greater(t, limit, 40, "fixture must be large enough to exercise truncation")

	produceCacheKey := func() string {
		setTruncateLimit(proxy, limit)
		result, err := proxy.handleRetrieveTools(pinned, retrieveReq())
		require.NoError(t, err)
		text := resultText(t, result)
		setTruncateLimit(proxy, 1_000_000)
		match := cacheKeyRE.FindStringSubmatch(text)
		require.Len(t, match, 2, "the truncated response must carry a read_cache key: %s", text)
		return match[1]
	}

	t.Run("policy fingerprint edit invalidates a cached page (ProfileServers unchanged)", func(t *testing.T) {
		key := produceCacheKey()

		control := readCacheReq(key)
		require.False(t, control.IsError, "control: the same authorization must redeem its own entry")

		cfgCopy := *rt.Config()
		cfg := &cfgCopy
		newProfiles := append([]config.ProfileConfig{}, cfg.Profiles...)
		for i := range newProfiles {
			if newProfiles[i].Name != "work-readonly" {
				continue
			}
			edited := *newProfiles[i].Tools
			edited.Deny = append(append([]string{}, edited.Deny...), "github:list_issues")
			newProfiles[i].Tools = &edited
		}
		cfg.Profiles = newProfiles
		rt.UpdateConfig(cfg, "")

		again := readCacheReq(key)
		assert.True(t, again.IsError, "editing the profile's tool policy must invalidate the page even though its server set did not change")
	})

	t.Run("out-of-band tool re-annotation (ToolTierGeneration) invalidates a cached page with no config edit", func(t *testing.T) {
		key := produceCacheKey()

		control := readCacheReq(key)
		require.False(t, control.IsError, "control: the same authorization must redeem its own entry")

		before := rt.Supervisor().ToolTierGeneration()

		// This test harness (startCountingUpstream) hand-writes StateView
		// directly and never runs the Supervisor's own reconcile loop, so its
		// internal ServerStateSnapshot never learns "github" exists —
		// RefreshServerToolsFromDiscovery treats an unknown server as
		// unconditionally accepted (publishDiscoveredTools's `!exists`
		// branch), which is exactly the "out of band" shape this test wants:
		// a zero-value capture is enough. Re-list github's tools with
		// cache_item_a now DESTRUCTIVE instead of read-only — no config
		// change at all.
		reannotated := []*config.ToolMetadata{
			{Name: "cache_item_a", ServerName: "github", Description: "cachepagetest", Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}},
			{Name: "cache_item_b", ServerName: "github", Description: "cachepagetest", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
			{Name: "cache_item_c", ServerName: "github", Description: "cachepagetest", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		}
		published, err := rt.Supervisor().RefreshServerToolsFromDiscovery("github", reannotated, supervisor.DiscoveryCapture{})
		require.NoError(t, err)
		require.True(t, published, "fixture: the re-list must land (github is unknown to the Supervisor's own reconcile state)")

		assert.Greater(t, rt.Supervisor().ToolTierGeneration(), before, "ToolTierGeneration must be bumped by the republish")

		again := readCacheReq(key)
		assert.True(t, again.IsError, "a tool re-annotation must invalidate the cached page even with no config change")
	})
}
