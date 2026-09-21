package server

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// scopeLatencyResultsFileEnv names the file .github/workflows/scope-latency.yml
// (T112a) points at when it runs this suite on the merge-base and HEAD
// checkouts in turn: when set, every operation below appends one
// "<operation>=<admin_p95_nanoseconds>" line, so the workflow's Go comparison
// program (cmd/scope-latency-compare, T112a) can diff the two revisions'
// administrator p95 without parsing test output. Unset (the default, every
// local/PR run) this is a no-op — the in-run scoped-vs-admin delta assertion
// below is the gate that always runs.
const scopeLatencyResultsFileEnv = "SCOPE_LATENCY_RESULTS_FILE"

var scopeLatencyResultsMu sync.Mutex

// recordAdminLatencyResult appends "<op>=<nanoseconds>" to the file named by
// scopeLatencyResultsFileEnv, if set.
func recordAdminLatencyResult(t *testing.T, op string, adminP95 time.Duration) {
	t.Helper()
	path := os.Getenv(scopeLatencyResultsFileEnv)
	if path == "" {
		return
	}
	scopeLatencyResultsMu.Lock()
	defer scopeLatencyResultsMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s=%d\n", op, adminP95.Nanoseconds())
	require.NoError(t, err)
}

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
	recordAdminLatencyResult(t, "retrieve_tools", p95Admin)

	const budget = 20 * time.Millisecond
	require.LessOrEqualf(t, gap, budget,
		"scoped retrieve_tools p95 must not exceed admin's by more than FR-011's %s budget (got admin=%s scoped=%s gap=%s)",
		budget, p95Admin, p95Scoped, gap)
}

// Spec 105 PR H1, T112: finalises the FR-011 latency harness over the
// remaining three operations (retrieve_tools is T078/T078 above) — 20
// warm-up + 200 timed calls per caller, p95(scoped) - p95(admin) <= 20ms per
// operation. Frozen fixtures: a 10-page cache entry and a 50-prompt set,
// built deterministically in code (not randomised) beside the existing
// 527-tool LiveMCPBench snapshot at internal/server/testdata/scope_latency/
// — see that directory's README for why the fixtures are generated in code
// rather than as separate serialized files (T112a's merge-base job copies
// this whole test file across revisions, which carries the generator code
// with it exactly as a serialized file would). Skipped under -race for the
// same reason as T078.

// measureLatency runs warm-up then timed calls of fn under ctx and returns
// the timed durations.
func measureLatency(t *testing.T, ctx context.Context, warmup, timed int, fn func(context.Context) error) []time.Duration {
	t.Helper()
	for i := 0; i < warmup; i++ {
		require.NoError(t, fn(ctx))
	}
	durations := make([]time.Duration, 0, timed)
	for i := 0; i < timed; i++ {
		start := time.Now()
		require.NoError(t, fn(ctx))
		durations = append(durations, time.Since(start))
	}
	return durations
}

// assertScopedWithinBudget is the FR-011 SC-006 assertion shared by every
// operation below.
func assertScopedWithinBudget(t *testing.T, op string, scoped, admin []time.Duration) {
	t.Helper()
	pScoped, pAdmin := p95(scoped), p95(admin)
	gap := pScoped - pAdmin
	t.Logf("%s p95 over %d timed calls: admin=%s scoped=%s gap=%s", op, len(scoped), pAdmin, pScoped, gap)
	recordAdminLatencyResult(t, op, pAdmin)
	const budget = 20 * time.Millisecond
	require.LessOrEqualf(t, gap, budget,
		"%s: scoped p95 must not exceed admin's by more than FR-011's %s budget (got admin=%s scoped=%s gap=%s)",
		op, budget, pAdmin, pScoped, gap)
}

// TestScopeLatency_ReadCache_ScopedVsAdmin: a frozen, oversized cache entry
// (10 truncated pages' worth of content) produced by the SAME scoped caller
// that reads it back, so both callers hit a live, redeemable key — the
// comparison is about the AUTHORIZATION CHECK's own cost, not about one
// caller hitting a refusal fast-path the other doesn't.
func TestScopeLatency_ReadCache_ScopedVsAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — 440 read_cache calls")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector's instrumentation overhead")
	}
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
	scopedCtx := agentCtx([]string{"a"}, []string{auth.PermRead}, "")

	// A frozen "10-page" entry: 10 records of deterministic padded content,
	// large enough that read_cache pages it rather than returning it whole.
	records := make([]string, 0, 10*50)
	for page := 0; page < 10; page++ {
		for i := 0; i < 50; i++ {
			records = append(records, `{"id":"`+recordID(page, i)+`","note":"frozen scope-latency fixture padding padding padding padding"}`)
		}
	}
	content := "[" + joinStrings(records, ",") + "]"
	stamp := proxy.cacheAuthorization(scopedCtx)
	const key = "scope-latency-frozen-key-0000000000000000000000000000000000000"
	require.NoError(t, proxy.cacheManager.StoreAs(key, "retrieve_tools",
		map[string]interface{}{"query": "frozen"}, content, "", len(records), stamp))

	call := func(ctx context.Context) error {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"key": key, "offset": float64(0), "limit": float64(50)}
		result, err := proxy.handleReadCache(ctx, req)
		if err != nil {
			return err
		}
		if result.IsError {
			return errIsError
		}
		return nil
	}

	adminDurations := measureLatency(t, adminCtx(), 20, 200, call)
	scopedDurations := measureLatency(t, scopedCtx, 20, 200, call)
	assertScopedWithinBudget(t, "read_cache", scopedDurations, adminDurations)
}

// connectLatencyPromptUpstream wires ONE real streamable-HTTP upstream
// exposing the given prompts and runs RefreshPrompts once. Deliberately
// self-contained (does not call scope_retained_effects_test.go's
// connectPromptUpstream): .github/workflows/scope-latency.yml (T112a) copies
// ONLY this file plus testdata/scope_latency/ over a merge-base checkout
// that does not have scope_retained_effects_test.go at all (that file is
// new in this same PR) — a cross-file helper dependency would make the
// copied file fail to compile there (cross-model review round 1 caught this
// exact break). internal/server/scope_fixture_test.go (created by an
// EARLIER, already-merged PR) is present on both sides, but its helpers are
// tool-oriented, not prompt-oriented, so this file carries its own minimal
// prompt-upstream wiring instead of depending on either.
func connectLatencyPromptUpstream(t *testing.T, proxy *MCPProxyServer, server string, prompts []mcp.Prompt) {
	t.Helper()
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true
	qOff := false
	proxy.config.QuarantineEnabled = &qOff

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithPromptCapabilities(true))
	for _, p := range prompts {
		text := p.Description
		mcpSrv.AddPrompt(p, func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{
				{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: text}},
			}}, nil
		})
	}
	testServer := servertest.NewTestStreamableHTTPServer(mcpSrv)
	t.Cleanup(testServer.Close)
	require.NoError(t, um.AddServerConfig(server, &config.ServerConfig{
		Name: server, Protocol: "streamable-http", URL: testServer.URL, Enabled: true,
	}))
	client, ok := um.GetClient(server)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, client.Connect(ctx))
	cancel()

	proxy.upstreamManager = um
	proxy.RefreshPrompts()
}

// TestScopeLatency_PromptsList_ScopedVsAdmin: a frozen 50-prompt set (one
// real upstream connection, so RefreshPrompts runs the actual aggregation
// pipeline once), then 220 in-process prompts/list calls per caller kind.
func TestScopeLatency_PromptsList_ScopedVsAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — 440 prompts/list calls")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector's instrumentation overhead")
	}
	proxy, _ := createTestProxyWithRuntime(t, nil)
	prompts := make([]mcp.Prompt, 0, 50)
	for i := 0; i < 50; i++ {
		prompts = append(prompts, mcp.Prompt{Name: "frozen_prompt_" + recordID(0, i), Description: "frozen scope-latency fixture prompt"})
	}
	connectLatencyPromptUpstream(t, proxy, "a", prompts)

	scopedCtx := agentCtx([]string{"a"}, []string{auth.PermRead}, "")
	call := func(ctx context.Context) error {
		raw := proxy.server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
		if raw == nil {
			return errIsError
		}
		return nil
	}
	adminDurations := measureLatency(t, adminCtx(), 20, 200, call)
	scopedDurations := measureLatency(t, scopedCtx, 20, 200, call)
	assertScopedWithinBudget(t, "prompts/list", scopedDurations, adminDurations)
}

// TestScopeLatency_ToolsList_ScopedVsAdmin: the direct surface's tools/list
// over the 527-tool LiveMCPBench snapshot, in-process through
// directServer.HandleMessage.
//
// Cross-model review (round 1) caught that an earlier version of this test
// only indexed the corpus into bleve (proxy.index.BatchIndexTools) — the
// SEARCH surface's data source — while the direct surface's tools/list reads
// from p.directServer's REGISTERED tool set, built by RefreshDirectModeTools
// from upstreamManager.DiscoverTools, which requires LIVE connected clients.
// Without a live connection per server the direct catalog stayed at the
// handful of built-ins, so the "527-tool" claim measured an empty listing.
// Fixed by connecting one real streamable-HTTP stub server per distinct
// ServerName in the corpus (grouped, not one-by-one) and publishing them
// through the real RefreshDirectModeTools rebuild ONCE before the timed
// loop — the 220 in-process tools/list calls per caller then read the
// already-published in-memory registry (no network in the timed path),
// which is the listing/filtering cost this test is meant to measure.
func TestScopeLatency_ToolsList_ScopedVsAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — 70 real upstream connections + 440 tools/list calls")
	}
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector's instrumentation overhead")
	}
	tools := loadDeferredLargeCorpus(t)

	byServer := make(map[string][]*config.ToolMetadata)
	var serverOrder []string
	for _, tool := range tools {
		if _, ok := byServer[tool.ServerName]; !ok {
			serverOrder = append(serverOrder, tool.ServerName)
		}
		byServer[tool.ServerName] = append(byServer[tool.ServerName], tool)
	}
	require.Greater(t, len(serverOrder), 10, "fixture: the snapshot must name more than a handful of servers")

	proxy, rt := createTestProxyWithRuntime(t, nil)
	qOff := false
	proxy.config.QuarantineEnabled = &qOff // a listing-throughput fixture has no interest in per-tool approval state

	for _, serverName := range serverOrder {
		specs := make([]toolSpec, 0, len(byServer[serverName]))
		for _, tool := range byServer[serverName] {
			specs = append(specs, readSpec(tool.Name))
		}
		startCountingUpstream(t, proxy, rt, serverName, specs...)
	}
	proxy.RefreshDirectModeTools()

	listed := proxy.directServer.ListTools()
	require.Greater(t, len(listed), 500,
		"fixture: the direct catalog must actually carry the ~527-tool corpus, not just the built-ins (got %d)", len(listed))

	scopedCtx := agentCtx([]string{serverOrder[0]}, []string{auth.PermRead}, "")
	call := func(ctx context.Context) error {
		raw := proxy.directServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if raw == nil {
			return errIsError
		}
		return nil
	}
	adminDurations := measureLatency(t, adminCtx(), 20, 200, call)
	scopedDurations := measureLatency(t, scopedCtx, 20, 200, call)
	assertScopedWithinBudget(t, "tools/list", scopedDurations, adminDurations)
}

var errIsError = &latencyIsError{}

type latencyIsError struct{}

func (*latencyIsError) Error() string { return "call returned an application-level error result" }

// recordID formats a deterministic, zero-padded id from (page, i) without
// pulling in fmt.Sprintf on a hot path used 20,000+ times across this file's
// three fixture builders.
func recordID(page, i int) string {
	return itoaPadded(page) + "-" + itoaPadded(i)
}

func itoaPadded(n int) string {
	s := ""
	if n == 0 {
		return "00"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	for len(s) < 2 {
		s = "0" + s
	}
	return s
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += sep
		}
		out += it
	}
	return out
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
