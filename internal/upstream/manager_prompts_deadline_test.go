package upstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 PR H1, T108a: the "shared prompt-refresh deadline" retained
// effect (spec.md:115) — internal/server.MCPProxyServer.RefreshPrompts wraps
// the WHOLE fleet's Manager.ListPrompts call in one
// context.WithTimeout(context.Background(), 30*time.Second) (hardcoded, not
// config, not test-injectable), and ListPrompts visits every connected
// server SEQUENTIALLY against that SAME shared context — so a slow server
// can exhaust the deadline before a later one in iteration order is ever
// visited, and that later server's prompts silently drop out of the
// published set until the next refresh.
//
// Reproducing the 30-second production deadline live would mean this test
// either waits out 30 real seconds or forks RefreshPrompts to accept an
// injectable one (internal/server/scope_retained_effects_test.go's header
// comment explains why neither is worth it). This test proves the SAME
// mechanism — sequential per-server calls sharing one caller-supplied
// context — directly against Manager.ListPrompts with an artificially short
// deadline, which RefreshPrompts's real 30s call is only a longer instance
// of: ListPrompts itself takes ctx from its caller and does nothing
// 30-second-specific.
//
// Cross-model review round 1's fixture used ONE slow server + one fast one
// and only asserted the slow server's own prompts never appeared — round 2
// correctly flagged that this does not distinguish a SHARED deadline from
// N INDEPENDENT-SEQUENTIAL per-server deadlines: either design would also
// keep the slow server's own late prompts out. Round 3 additionally noted
// that in THIS fixture's specific parameter regime (a 60ms budget far
// shorter than the 300ms slow delay), an independent-CONCURRENT design
// (every server racing against its OWN fresh 60ms, in parallel) is ALSO
// indistinguishable from shared-sequential here: neither lets any 300ms
// server finish inside a 60ms window, so both designs equally admit only
// the fast server and bound elapsed to ~60ms. This fixture therefore rules
// out independent-SEQUENTIAL (via the elapsed-time bound below: summing N
// independent 60ms budgets in turn would take ~Nx60ms, not ~60ms) but NOT
// independent-CONCURRENT — that gap is closed by the companion fixture,
// TestManager_ListPrompts_SharedBudgetCannotFitAllServersEvenWithGenerousDeadline
// below, whose 150ms-budget/100ms-delay parameters are chosen specifically
// so an independent-concurrent design (every server's own generous 150ms
// budget easily fits its 100ms of work, all racing in parallel) WOULD let
// every server succeed, while the real shared-sequential mechanism cannot.
// The two fixtures together rule out both alternative designs.
//
// Go map iteration order (m.clients) is genuinely random and not
// controllable from a test, so this fixture uses ENOUGH slow servers (four,
// each individually slower than the shared deadline) that REGARDLESS of
// which one m.clients visits first, that first slow server alone consumes
// the entire shared budget. The deterministic, order-independent invariants
// this proves:
//
//   - no slow server's own prompts EVER appear (true under shared,
//     independent-sequential AND independent-concurrent designs alike in
//     this parameter regime — this alone is round 1's weaker claim);
//   - AT MOST ONE server's prompts appear in total, and only when that one
//     server (fast or one particular slow one) happened to be visited
//     FIRST — an independent-SEQUENTIAL-per-server-deadline design would
//     instead let EVERY slow server eventually succeed on its own fresh
//     budget in turn, so seeing more than one server's prompts would prove
//     that alternative rather than sharing;
//   - total elapsed time stays close to ONE deadline's worth of budget
//     regardless of how many slow servers are configured — four
//     independent 60ms budgets applied sequentially would sum to ~240ms if
//     each is exhausted in turn (the mechanism this test rules out), while
//     one SHARED 60ms budget bounds the whole call to ~60ms however many
//     servers are configured.
func TestManager_ListPrompts_SharedNotPerServerDeadline(t *testing.T) {
	m := newTestManager(t)

	const slowServerDelay = 300 * time.Millisecond
	const sharedDeadline = 60 * time.Millisecond // shorter than slowServerDelay, longer than a loopback round trip
	const slowServerCount = 4                    // enough that whichever one m.clients visits first, that one alone exhausts the shared budget

	slowServerNames := make(map[string]bool, slowServerCount)
	for i := 0; i < slowServerCount; i++ {
		id := fmt.Sprintf("slow%d", i)
		name := fmt.Sprintf("slow-server-%d", i)
		promptName := fmt.Sprintf("slow_prompt_%d", i)
		slowServerNames[name+":"+promptName] = true

		slowUpstream := mcpserver.NewMCPServer(name, "0.0.1",
			mcpserver.WithPromptCapabilities(true),
			mcpserver.WithHooks(func() *mcpserver.Hooks {
				h := &mcpserver.Hooks{}
				h.AddBeforeListPrompts(func(ctx context.Context, _ any, _ *mcp.ListPromptsRequest) {
					select {
					case <-time.After(slowServerDelay):
					case <-ctx.Done():
					}
				})
				return h
			}()),
		)
		slowUpstream.AddPrompt(mcp.NewPrompt(promptName, mcp.WithPromptDescription("from a slow server")),
			func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "ok"}}}}, nil
			})
		slowServer := servertest.NewTestStreamableHTTPServer(slowUpstream)
		t.Cleanup(slowServer.Close)
		require.NoError(t, m.AddServerConfig(id, &config.ServerConfig{
			Name: name, Protocol: "streamable-http", URL: slowServer.URL, Enabled: true,
		}))
		client, ok := m.GetClient(id)
		require.True(t, ok)
		connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, client.Connect(connectCtx))
		cancel()
	}

	addConnectedTestServer(t, m, "fast", "fast-server", "fast_prompt")
	const fastPromptName = "fast-server:fast_prompt"

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), sharedDeadline)
	defer cancel()
	prompts, err := m.ListPrompts(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err, "a per-client failure (including a context-deadline error) is logged and skipped, never returned")

	names := make(map[string]bool, len(prompts))
	for _, p := range prompts {
		names[p.Name] = true
	}
	t.Logf("shared-deadline fixture (%d slow servers): elapsed=%s prompts=%v", slowServerCount, elapsed, names)

	for name := range slowServerNames {
		assert.False(t, names[name], "no slow server's own prompts may ever survive the shared deadline, got %q present", name)
	}
	assert.LessOrEqualf(t, len(prompts), 1,
		"AT MOST ONE server's prompts may appear: with %d slow servers configured (each individually slower than the shared deadline), an INDEPENDENT-SEQUENTIAL-per-server-deadline design would let every one of them eventually succeed in turn on its own fresh budget — seeing %d servers' worth of prompts (%v) would prove that alternative, not sharing. (The independent-CONCURRENT alternative is ruled out separately by TestManager_ListPrompts_SharedBudgetCannotFitAllServersEvenWithGenerousDeadline's parameters, not this one.)", slowServerCount, len(prompts), names)
	if len(prompts) == 1 {
		assert.True(t, names[fastPromptName],
			"the one server whose prompts survived must be the fast one — a slow server surviving would mean its own 300ms call completed inside the shared 60ms budget, which is impossible unless the budget were not actually shared/bounded")
	}
	// Four INDEPENDENT 60ms per-server budgets applied sequentially (the
	// design this test rules out) would sum to ~240ms as each slow server
	// exhausts its own allowance in turn; a SHARED 60ms budget bounds the
	// WHOLE call to ~60ms regardless of how many slow servers are
	// configured. The generous ceiling (2x the shared deadline, still far
	// below the 4x-per-server-budget sum) absorbs scheduler/loopback jitter
	// without weakening what it rules out.
	assert.Less(t, elapsed, 2*sharedDeadline,
		"the shared deadline must bound the WHOLE ListPrompts call to roughly ONE deadline's worth of time regardless of how many slow servers are configured — %s with %d slow servers looks like %d independent per-server budgets summing up, not one shared budget",
		elapsed, slowServerCount, slowServerCount)
}

// TestManager_ListPrompts_SharedBudgetCannotFitAllServersEvenWithGenerousDeadline
// is the SECOND discriminating regime cross-model review round 3 asked for.
// Round 2's fixture (a 60ms deadline far shorter than the 300ms slow delay)
// cannot tell a SHARED-SEQUENTIAL budget apart from N INDEPENDENT,
// CONCURRENTLY-RACED per-server deadlines of the same size: both designs
// would let only the fast server (or whichever happens to start first)
// through and bound total elapsed to ~one deadline, because EVERY slow
// server individually exceeds even its own hypothetical full budget in
// either design.
//
// This regime instead uses a deadline BETWEEN one and two slow servers'
// worth of work (150ms, with five 100ms-each slow servers, no fast server
// at all): an INDEPENDENT-CONCURRENT design would give every one of the five
// slow servers its own fresh 150ms budget, racing in parallel — 100ms of
// real work comfortably fits, so all five would succeed, in ~100ms total.
// An INDEPENDENT-SEQUENTIAL design would eventually let all five succeed
// too (each gets a fresh budget when its turn comes), just taking ~500ms
// total. Only the ACTUAL shared-sequential design — one 150ms budget spent
// as servers are visited in turn — admits at most ONE full slow server
// (100ms) before the shared clock has too little left (150-100=50ms) for a
// second 100ms server, so it can never seat more than one or two (a second
// one only if it happened to be far enough along when the deadline hit,
// which this test does not rely on): asserting "well under all five
// succeeded" rules out BOTH alternative designs at once, closing the gap
// round 3 identified in the single-regime version of this test.
func TestManager_ListPrompts_SharedBudgetCannotFitAllServersEvenWithGenerousDeadline(t *testing.T) {
	m := newTestManager(t)

	const perServerDelay = 100 * time.Millisecond
	const sharedDeadline = 150 * time.Millisecond // fits ONE full slow server, not two
	const slowServerCount = 5

	for i := 0; i < slowServerCount; i++ {
		id := fmt.Sprintf("budget-slow%d", i)
		name := fmt.Sprintf("budget-slow-server-%d", i)
		promptName := fmt.Sprintf("budget_slow_prompt_%d", i)

		slowUpstream := mcpserver.NewMCPServer(name, "0.0.1",
			mcpserver.WithPromptCapabilities(true),
			mcpserver.WithHooks(func() *mcpserver.Hooks {
				h := &mcpserver.Hooks{}
				h.AddBeforeListPrompts(func(ctx context.Context, _ any, _ *mcp.ListPromptsRequest) {
					select {
					case <-time.After(perServerDelay):
					case <-ctx.Done():
					}
				})
				return h
			}()),
		)
		slowUpstream.AddPrompt(mcp.NewPrompt(promptName, mcp.WithPromptDescription("from a budget-test slow server")),
			func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "ok"}}}}, nil
			})
		slowServer := servertest.NewTestStreamableHTTPServer(slowUpstream)
		t.Cleanup(slowServer.Close)
		require.NoError(t, m.AddServerConfig(id, &config.ServerConfig{
			Name: name, Protocol: "streamable-http", URL: slowServer.URL, Enabled: true,
		}))
		client, ok := m.GetClient(id)
		require.True(t, ok)
		connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, client.Connect(connectCtx))
		cancel()
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), sharedDeadline)
	defer cancel()
	prompts, err := m.ListPrompts(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)

	t.Logf("budget fixture (%d servers x %s each, shared budget %s): elapsed=%s succeeded=%d/%d",
		slowServerCount, perServerDelay, sharedDeadline, elapsed, len(prompts), slowServerCount)

	assert.LessOrEqualf(t, len(prompts), 2,
		"a %s shared budget must not fit more than ~1 of %d servers each needing %s: got %d succeeded — both an independent-CONCURRENT design (every server gets its own fresh %s, all fit) and an independent-SEQUENTIAL design (every server eventually gets a fresh %s) would let ALL %d succeed, which this must rule out",
		sharedDeadline, slowServerCount, perServerDelay, len(prompts), sharedDeadline, sharedDeadline, slowServerCount)
	assert.Less(t, elapsed, time.Duration(slowServerCount)*perServerDelay,
		"elapsed %s must be far below the %s an independent-sequential design would take to eventually seat all %d servers",
		elapsed, time.Duration(slowServerCount)*perServerDelay, slowServerCount)
}
