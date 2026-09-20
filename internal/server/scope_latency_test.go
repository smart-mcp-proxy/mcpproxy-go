package server

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 PR C, T078 (FR-011 pre-check).
//
// SearchToolsScoped (index/bleve.go) pages the ranked result exhaustively
// with NO cap, by design (FR-010's existence-oracle rule forbids one) — so a
// scoped retrieve_tools call does strictly more work than the unscoped
// Search(query, limit) an administrator's call makes. This is a prototype
// measurement, not a CI gate: it runs once, locally, on the 527-tool
// LiveMCPBench snapshot the Spec 102/083 fleet-scale tests already use, and
// asserts the scoped/admin p95 gap for retrieve_tools stays within FR-011's
// 20ms budget on this machine. H1 is the follow-up that extends this to the
// other three FR-011 operations (read_cache, prompts/list, tools/list) and
// wires a CI job with a merge-base comparison; this file's job is only to
// prove the gap is small enough that H1 is worth building, and to record a
// local number in the PR body.
//
// Skipped under -race: the race detector's instrumentation overhead swamps
// the microsecond-scale differences this test measures, and (per the shared
// raceEnabled convention elsewhere in this package) makes the timing
// meaningless rather than merely slower.
func TestRetrieveTools_ScopeLatency_ScopedVsAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — 220 retrieve_tools calls per caller kind")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector's instrumentation overhead")
	}

	tools := loadDeferredLargeCorpus(t)
	proxy := createTestMCPProxyServer(t)

	seenServers := make(map[string]bool)
	var servers []string
	for _, tool := range tools {
		if !seenServers[tool.ServerName] {
			seenServers[tool.ServerName] = true
			servers = append(servers, tool.ServerName)
			require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
				Name: tool.ServerName, Enabled: true,
			}))
		}
	}
	require.NoError(t, proxy.index.BatchIndexTools(tools))
	require.Greater(t, len(servers), 10, "fixture: the snapshot must name more than a handful of servers")

	// A deliberately narrow scope — one real server out of the fleet — is the
	// worst case for the exhaustive scoped scan: almost every ranked hit is
	// filtered out before the window fills.
	scopedCtx := agentCtx([]string{servers[0]}, []string{auth.PermRead}, "")
	adminScopeCtx := adminCtx()

	const query = "get data"
	const limit = 10
	const warmup = 20
	const timed = 200

	measure := func(ctx context.Context) []time.Duration {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"query": query, "limit": float64(limit)}

		for i := 0; i < warmup; i++ {
			_, err := proxy.handleRetrieveTools(ctx, req)
			require.NoError(t, err)
		}

		durations := make([]time.Duration, 0, timed)
		for i := 0; i < timed; i++ {
			start := time.Now()
			_, err := proxy.handleRetrieveTools(ctx, req)
			durations = append(durations, time.Since(start))
			require.NoError(t, err)
		}
		return durations
	}

	scopedDurations := measure(scopedCtx)
	adminDurations := measure(adminScopeCtx)

	p95Scoped := p95(scopedDurations)
	p95Admin := p95(adminDurations)
	gap := p95Scoped - p95Admin

	t.Logf("retrieve_tools p95 over %d timed calls (527-tool snapshot, %d servers, scope=1 server): admin=%s scoped=%s gap=%s",
		timed, len(servers), p95Admin, p95Scoped, gap)

	const budget = 20 * time.Millisecond
	require.LessOrEqualf(t, gap, budget,
		"scoped retrieve_tools p95 must not exceed admin's by more than FR-011's %s budget (got admin=%s scoped=%s gap=%s)",
		budget, p95Admin, p95Scoped, gap)
}

// p95 returns the 95th-percentile duration, sorting a copy so the caller's
// slice order is left intact.
func p95(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted))*0.95) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
