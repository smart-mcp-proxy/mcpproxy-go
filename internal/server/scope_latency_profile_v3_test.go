package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108 (Profiles v3) T025 (SC-007): the v3 policy-aware SearchToolsAdmitted
// path resolves one EffectiveAnnotations lookup and one CompiledPolicy.Decide
// call per scanned hit, on top of the plain server-scope check
// SearchToolsScoped already paid for (Spec 105 T078). It measures over a
// 1,000+-tool corpus (the Spec 102/083 LiveMCPBench snapshot, doubled under a
// second server-name suffix to clear the SC-007 scale) and asserts the
// v3-vs-legacy p95 gap stays within FR-011's 20ms budget in the same run.
//
// The name matches the `TestScopeLatency_` regex of
// .github/workflows/scope-latency.yml, so that advisory job (continue-on-error)
// runs it on HEAD; the v3 measurement is informational there and is not one of
// cmd/scope-latency-compare's requiredOperations.
//
// Skipped under -race and testing.Short() for the same reasons as its Spec
// 105 sibling in scope_latency_test.go.
func TestScopeLatency_ProfileV3VsLegacy(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — 400+ retrieve_tools calls over a 1,000+-tool corpus")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector's instrumentation overhead")
	}

	base := loadDeferredLargeCorpus(t)
	// Double the corpus under a second server-name suffix to clear the
	// SC-007 1,000-tool scale without hand-authoring a synthetic fixture.
	tools := make([]*config.ToolMetadata, 0, len(base)*2)
	tools = append(tools, base...)
	for _, tool := range base {
		dup := *tool
		dup.ServerName = tool.ServerName + "_dup"
		dup.Name = dup.ServerName + ":" + config.RawToolName(tool)
		dup.RawName = config.RawToolName(tool)
		tools = append(tools, &dup)
	}
	require.GreaterOrEqual(t, len(tools), 1000, "fixture must reach the SC-007 1,000-tool scale")

	seenServers := make(map[string]bool)
	var servers []string
	for _, tool := range tools {
		if !seenServers[tool.ServerName] {
			seenServers[tool.ServerName] = true
			servers = append(servers, tool.ServerName)
		}
	}
	require.Greater(t, len(servers), 20, "fixture: doubling the snapshot must still name more than a handful of servers")

	proxy := createTestMCPProxyServer(t)
	serverCfgs := make([]*config.ServerConfig, 0, len(servers))
	for _, s := range servers {
		serverCfgs = append(serverCfgs, &config.ServerConfig{Name: s, Enabled: true})
	}
	proxy.config.Servers = serverCfgs
	proxy.config.Profiles = []config.ProfileConfig{
		{Name: "v3-cap-read", Servers: servers, MaxTier: "read", Unannotated: "as_read"},
		{Name: "legacy-samescope", Servers: servers},
	}
	require.NoError(t, proxy.index.BatchIndexTools(tools))
	// zcode review F3: retrieve_tools resolves searchIndex via
	// index.ForProfile(profileName) — a lazily-created, initially EMPTY
	// per-profile store that is populated only by RebuildProfileFromShared.
	// BatchIndexTools above only reaches the SHARED default index, so
	// without this the per-profile search below scans zero hits for BOTH
	// contexts: the policy path's actual per-hit cost (EffectiveAnnotations
	// + CompiledPolicy.Decide) is never paid, and the asserted p95 gap is
	// measuring two no-op searches against each other.
	require.NoError(t, proxy.index.RebuildProfileFromShared("v3-cap-read", servers))
	require.NoError(t, proxy.index.RebuildProfileFromShared("legacy-samescope", servers))

	const query = "get data"
	const limit = 10
	const warmup = 20
	const timed = 200

	legacyCtx := profile.WithProfileScope(context.Background(), proxy.profileScopeForSlug("legacy-samescope"))
	v3Ctx := profile.WithProfileScope(context.Background(), proxy.profileScopeForSlug("v3-cap-read"))

	// Fixture sanity (zcode review F3): the per-profile PHYSICAL INDEX each
	// context searches must actually hold real, matching documents, or the
	// p95 gap asserted below is measuring two no-op scans against each
	// other rather than the policy path's real per-hit cost. Checked via a
	// direct index search — NOT retrieve_tools' own JSON `total` — because
	// this synthetic corpus's servers are indexed but never connected to a
	// live upstream, so retrieve_tools' unrelated callability gate (no
	// server is "callable" without a real connection) empties tools[]
	// downstream of the scan this test cares about, for both profiles
	// alike; that gate is orthogonal to whether SearchToolsAdmitted itself
	// paid the EffectiveAnnotations+CompiledPolicy.Decide cost per hit.
	for _, slug := range []string{"legacy-samescope", "v3-cap-read"} {
		pIdx, err := proxy.index.ForProfile(slug)
		require.NoError(t, err)
		hits, err := pIdx.Search(query, limit)
		require.NoError(t, err)
		require.NotEmptyf(t, hits,
			"%s: per-profile index must hold real matching documents, not an empty ForProfile index never populated by RebuildProfileFromShared", slug)
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": query, "limit": float64(limit)}
	// Arms interleaved (measureInterleaved) so a runner noise burst cannot land
	// on one arm alone.
	legacyDurations, v3Durations := measureInterleaved(t, legacyCtx, v3Ctx, warmup, timed,
		func(ctx context.Context) error {
			_, err := proxy.handleRetrieveTools(ctx, req)
			return err
		})

	pLegacy, pV3 := p95(legacyDurations), p95(v3Durations)
	gap := pV3 - pLegacy
	t.Logf("retrieve_tools p95 over %d timed calls (%d-tool corpus, %d servers): legacy=%s v3=%s gap=%s",
		timed, len(tools), len(servers), pLegacy, pV3, gap)
	recordAdminLatencyResult(t, "retrieve_tools_profile_v3", pV3)

	const budget = 20 * time.Millisecond
	require.LessOrEqualf(t, gap, budget,
		"v3 policy-aware retrieve_tools p95 must not exceed legacy's by more than FR-011's %s budget (got legacy=%s v3=%s gap=%s)",
		budget, pLegacy, pV3, gap)
}
