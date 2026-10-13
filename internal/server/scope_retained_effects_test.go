package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Spec 105 PR H1, T108a: the retained-effect mode of the differential oracle
// (contracts/differential-oracle.md, spec.md:114,161, SC-001's exclusion
// list). These are documented, DELIBERATELY UNCHANGED behaviours — a hidden
// server can still influence an authorized caller's outcome through shared
// infrastructure — so they are NOT run through runScopeScenario's
// cross-fixture byte-equality (that assertion would legitimately fail for
// every one of them, which is exactly why the spec excludes them from
// SC-001). Instead each fixture asserts:
//
//	(a) every item returned is owned by an authorized server,
//	(b) no hidden definition or content and no sentinel appears,
//	(c) the documented outcome (the specific refusal / pending / shared-effect
//	    state) is observed.
//
// Two of the seven named effects (prompt-name collision, the global prompt
// cap) depend on Go MAP ITERATION ORDER inside upstream.Manager.ListPrompts
// (internal/upstream/manager_prompts.go) — which server's prompt "wins" a
// display-name collision, or which server's prompts survive the
// maxAggregatedPrompts backstop, is randomised per process by design. Their
// fixtures assert the ORDER-INDEPENDENT invariant (a)+(b) rather than which
// specific outcome occurred on this run — asserting a specific winner would
// be exactly the kind of flaky gate CLAUDE.md warns against (PRs F and G
// already hit real flakiness from over-tight assertions today).
//
// Direct display-name collision (spec.md:115) is already pinned,
// deterministically, by internal/server/mcp_direct_catalog_test.go (gap-map
// "already satisfied" list); TestScopeRetainedEffect_DirectDisplayCollision
// below re-runs it as this suite's named fixture for that effect. Shared log
// rotation/retention (FR-007) is pinned by
// internal/logs.TestReadUpstreamServerLogTail_AttributedOnly_ForcedRotationSharedHistory
// — a different Go package, cited here rather than re-invoked (same
// constraint as the US1.7 inventory entry in scope_differential_test.go).
//
// Shared prompt-refresh deadline (spec.md:115) is NOT reproduced live at
// THIS layer: MCPProxyServer.RefreshPrompts (mcp_routing.go) wraps the WHOLE
// fleet's upstream.Manager.ListPrompts call in one hardcoded
// `context.WithTimeout(context.Background(), 30*time.Second)` — not a config
// value, not overridable by a test — so exercising the 30-SECOND deadline
// specifically through RefreshPrompts would mean this suite either waits out
// 30 real seconds or forks the production code path to accept an injectable
// one, either a worse trade than proving the mechanism one layer down (an
// over-tight or artificially-slow gate is exactly what CLAUDE.md's PR F/G
// flakiness lesson warns against). ListPrompts (manager_prompts.go) itself
// takes its ctx from the CALLER and does nothing 30-second-specific — the
// production deadline is just a longer instance of the same sequential-
// calls-sharing-one-context mechanism — so the actual named fixture lives at
// that layer, with an artificially SHORT injected deadline, in
// internal/upstream/manager_prompts_deadline_test.go:
// TestManager_ListPrompts_SharedNotPerServerDeadline
// (verified non-vacuous: a generous deadline in place of the short one makes
// both peers' prompts appear and the test correctly fails). The multi-server
// sequential-aggregation PATH is additionally exercised, deterministically
// and without any injected delay, by
// TestScopeRetainedEffect_PromptNameCollision and
// TestScopeRetainedEffect_GlobalPromptCap below, which both run REAL
// multi-server ListPrompts aggregation through RefreshPrompts at this layer.

// runRetainedEffectScenario is the alternative assertion mode for the
// documented retained effects (contracts/differential-oracle.md
// "Retained-effect mode"). fn builds its own fixture (each effect has a
// distinct one, per spec.md's fixture recipes) and returns the raw response
// text plus the sentinel(s) that must never appear in it.
func runRetainedEffectScenario(t *testing.T, name string, fn func(t *testing.T) (responseText string, sentinels []string)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		text, sentinels := fn(t)
		for _, s := range sentinels {
			assert.NotContains(t, text, s, "retained effect %q: no hidden content or sentinel may appear", name)
		}
	})
}

// ---------------------------------------------------------------------------
// 1. Shared-limiter contention — global capacity 1, no queue, a held call on
//    hidden "b" makes a call to authorized "a" fail with the existing
//    "proxy-wide limit saturated" response.
// ---------------------------------------------------------------------------

func TestScopeRetainedEffect_SharedLimiterContention(t *testing.T) {
	runRetainedEffectScenario(t, "shared-limiter-contention", func(t *testing.T) (string, []string) {
		one, zero := 1, 0
		proxy, rt := createTestProxyWithRuntimeCfg(t,
			[]*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}},
			func(c *config.Config) { c.MaxConcurrentRequests = &one; c.QueueSize = &zero })

		startCountingUpstream(t, proxy, rt, "a", readSpec("read_thing"))
		sentinel := "SENTINEL_limiter_b_4a1f"
		bUp := startCountingUpstream(t, proxy, rt, "b", readSpec("held"))

		started := make(chan struct{})
		release := make(chan struct{})
		var startOnce sync.Once
		bUp.mcpSrv.AddTool(mcp.Tool{Name: "held", Description: "Handles " + sentinel, InputSchema: mcp.ToolInputSchema{Type: "object"}},
			func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				startOnce.Do(func() { close(started) })
				<-release
				return mcp.NewToolResultText("ok"), nil
			})

		wildcard := agentCtx([]string{"*"}, allPerms, "")
		done := make(chan *mcp.CallToolResult, 1)
		go func() {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]interface{}{"name": "b:held", "args": map[string]interface{}{}}
			res, err := proxy.handleCallToolVariant(wildcard, req, contracts.ToolVariantRead)
			require.NoError(t, err)
			done <- res
		}()

		select {
		case <-started:
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("the held call on b never started")
		}

		// The a-only token's call to "a" must now be refused: the global
		// slot is saturated and there is no queue.
		aOnly := agentCtx([]string{"a"}, allPerms, "")
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"name": "a:read_thing", "args": map[string]interface{}{}}
		result, err := proxy.handleCallToolVariant(aOnly, req, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.True(t, result.IsError, "the call to 'a' must be shed while 'b' holds the only global slot")
		text := resultText(t, result)
		assert.Contains(t, text, "proxy-wide", "the documented outcome is the proxy-wide saturation refusal, not a scope refusal")
		assert.Contains(t, text, "saturated")

		close(release)
		<-done
		return text, []string{sentinel}
	})
}

// ---------------------------------------------------------------------------
// 2. Cross-server security-scan admission — real discovery with an
//    established baseline on "a", then an added tool that scans clean
//    without hidden "b" but has a matching near-identical peer on "b" under
//    trust_mode: scan.
// ---------------------------------------------------------------------------

// TestScopeRetainedEffect_CrossServerScanAdmission reproduces the ACTUAL
// shadowing mechanism (internal/security/detect/checks/shadowing.go
// "impersonation clone" shape), not just ordinary hidden-tool filtering.
// Cross-model review (round 1) caught that an earlier version of this test
// never created a same-name pair with near-identical descriptions and never
// asserted the pending outcome — it only proved retrieve_tools' ordinary
// scope filter, which every other differential test already covers.
//
// The real mechanism (internal/runtime/tool_quarantine.go
// checkToolApprovals, "new tool" branch, scanMode case): a NEW tool on a
// trust_mode:scan server with an already-established baseline runs
// scanChangeIsClean, which feeds the synchronous TPA scanner every OTHER
// connected server's CURRENT tools as peer context
// (collectPeerToolMetadata, sourced from the live StateView snapshot) so
// the shadowing.cross_server check can fire. That check's "impersonation
// clone" shape (shadowing.go cloneDescriptions) flags a tool whose NAME and
// DESCRIPTION both near-duplicate another server's tool of the same name —
// exactly the fixture spec.md names: "a same-name near-identical tool on
// hidden b". A non-clean verdict holds the new tool PENDING (fail closed)
// instead of auto-approving it via "scan-approved".
//
// Fixture: hidden "b" already exposes "sync_database" with a description at
// pass 1 (so it is a live StateView peer by the time "a" is rediscovered);
// "a" establishes its own unrelated baseline at pass 1, then gains its OWN
// "sync_database" tool — same name, a near-duplicate (word-order-shuffled)
// description — on pass 2, driven through the REAL discovery producer
// (up.serve + rt.RefreshServerTools, the same recipe
// TestToolGate_LegacyCollapsedRecord_* uses) so the scan gate genuinely
// runs with "b" as a live peer.
func TestScopeRetainedEffect_CrossServerScanAdmission(t *testing.T) {
	runRetainedEffectScenario(t, "cross-server-scan-admission", func(t *testing.T) (string, []string) {
		proxy, rt := createTestProxyWithRuntimeCfg(t,
			[]*config.ServerConfig{
				{Name: "a", Enabled: true, TrustMode: string(config.TrustModeScan)},
				{Name: "b", Enabled: true, TrustMode: string(config.TrustModeScan)},
			}, nil)

		sentinel := "SENTINEL_scanadmission_b_5c7a"
		const sharedToolName = "sync_database"
		// Both descriptions share the SAME 15-token base (only reordered),
		// so cloneDescriptions' token-containment bar (shared >= 0.85 of the
		// smaller set AND >= 0.7 of the larger) clears comfortably even
		// after "b"'s description picks up 4 extra, non-overlapping tokens
		// from the sentinel: shared=15, smaller=15, larger=19 -> 15/15=1.0,
		// 15/19=0.79 — both above their respective bars.
		aDescription := "Synchronize production database records to nightly encrypted cold storage backup bucket automatically without manual intervention"
		bDescription := "Synchronize database records to production nightly encrypted cold storage backup bucket automatically without manual intervention — " + sentinel

		// "b" exposes the peer tool from the start — it must be a live
		// StateView entry when "a"'s pass-2 scan gathers cross-server
		// context.
		startCountingUpstream(t, proxy, rt, "b", toolSpec{
			Name: sharedToolName, Description: bDescription,
			Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
		})

		// "a" establishes its own baseline (pass 1, no peer collision yet).
		aUp := startCountingUpstream(t, proxy, rt, "a", readSpec("baseline_tool"))

		// Pass 2: "a" gains the same-name, near-duplicate-description tool,
		// driven through the REAL discovery producer. rt.RefreshServerTools
		// reads through the RUNTIME's own upstream manager (createTestProxyWithRuntimeCfg
		// wires two — proxy's and the runtime's — exactly as
		// runRuntimeDiscovery's doc comment explains), so the runtime
		// manager must be connected to "a" itself before the refresh, the
		// same two-manager dance TestToolGate_LegacyCollapsedRecord_* uses.
		aUp.serve(toolSpec{
			Name: sharedToolName, Description: aDescription,
			Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
		})
		runRuntimeDiscovery(t, proxy, rt, aUp)

		record, err := proxy.storage.GetToolApproval("a", sharedToolName)
		require.NoError(t, err, "the new tool must have its own approval record after pass 2")
		assert.Equal(t, storage.ToolApprovalStatusPending, record.Status,
			"the shadowing check's impersonation-clone shape must hold a's new tool pending — a clean scan-approve here means the fixture's descriptions are not near-duplicate enough to trigger shadowing.cross_server")
		// isToolCallable is deliberately the SEARCH-visibility filter only
		// (mcp.go's isExactToolCallable checks Disabled, never pending —
		// documented there as "quarantine is deliberately NOT gated here");
		// the DISPATCH gate is evaluateToolGate, which is what actually
		// blocks a pending tool from executing.
		assert.False(t, proxy.evaluateToolGate("a", sharedToolName).callable(),
			"a pending tool must not be dispatchable — the retained effect changed a's OWN discovery/dispatch outcome, exactly as spec.md documents")

		// Ownership invariant: whatever the gate decided, an a-only token's
		// discovery of "a" must never surface "b"'s content, name or
		// sentinel.
		ctx := agentCtx([]string{"a"}, allPerms, "")
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"query": "synchronize database", "limit": float64(10)}
		result, err := proxy.handleRetrieveTools(ctx, req)
		require.NoError(t, err)
		require.False(t, result.IsError)
		text := resultText(t, result)
		assert.NotContains(t, text, "b:"+sharedToolName, "an a-only token must never see hidden b's tool by name")
		return text, []string{sentinel}
	})
}

// ---------------------------------------------------------------------------
// 3. Prompt-name collision and 4. global prompt cap
//    (internal/upstream/manager_prompts.go maxAggregatedPrompts=1000). Both
//    depend on Go's randomised map iteration order inside
//    upstream.Manager.ListPrompts, so the fixtures assert the
//    order-independent invariant rather than a specific winner.
// ---------------------------------------------------------------------------

// connectPromptUpstream wires a fresh upstream.Manager (mirroring
// TestAggregatedPrompt_ScopeUsesCanonicalOwner's pattern) with one real
// streamable-HTTP upstream per server name, each serving the given prompts,
// and installs it as proxy.upstreamManager.
func connectPromptUpstream(t *testing.T, proxy *MCPProxyServer, servers map[string][]mcp.Prompt) {
	t.Helper()
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true
	qOff := false
	proxy.config.QuarantineEnabled = &qOff

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	for name, prompts := range servers {
		mcpSrv := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithPromptCapabilities(true))
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
		require.NoError(t, um.AddServerConfig(name, &config.ServerConfig{
			Name: name, Protocol: "streamable-http", URL: testServer.URL, Enabled: true,
		}))
		client, ok := um.GetClient(name)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, client.Connect(ctx))
		cancel()
	}
	proxy.upstreamManager = um
	proxy.RefreshPrompts()
}

func TestScopeRetainedEffect_PromptNameCollision(t *testing.T) {
	runRetainedEffectScenario(t, "prompt-name-collision", func(t *testing.T) (string, []string) {
		proxy, _ := createTestProxyWithRuntime(t, nil)
		sentinel := "SENTINEL_promptcollide_9b2e"
		// Two distinct (server, prompt) pairs flatten to the SAME display
		// name "a__b__shared" — spec.md's own collision shape: server "a"
		// with prompt "b__shared", and hidden server "a__b" with prompt
		// "shared".
		connectPromptUpstream(t, proxy, map[string][]mcp.Prompt{
			"a":    {{Name: "b__shared", Description: "Authorized prompt"}},
			"a__b": {{Name: "shared", Description: sentinel}},
		})

		ctx := agentCtx([]string{"a"}, allPerms, "")
		registered := proxy.server.ListPrompts()
		var names strings.Builder
		for name := range registered {
			names.WriteString(name)
			names.WriteString(" ")
		}

		// Ownership invariant: whichever server's prompt won the collision,
		// fetching it through the REGISTERED handler (which runs the same
		// authorize() check the real prompts/get RPC does — calling the raw
		// getPromptAggregated helper directly would bypass that check and
		// prove nothing about caller-facing disclosure) must never yield
		// hidden a__b's content to this a-only token.
		if entry, ok := registered["a__b__shared"]; ok {
			result, err := entry.Handler(ctx, mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: "a__b__shared"}})
			if err == nil {
				for _, msg := range result.Messages {
					if tc, ok := msg.Content.(mcp.TextContent); ok {
						assert.NotContains(t, tc.Text, sentinel)
					}
				}
			}
		}
		return names.String(), []string{sentinel}
	})
}

// TestScopeRetainedEffect_GlobalPromptCap stresses the ACTUAL 1000-prompt
// aggregate backstop (internal/upstream/manager_prompts.go
// maxAggregatedPrompts), not just its arithmetic. Cross-model review (round
// 1) caught that a single hidden server's 250 prompts never reaches the
// aggregate cap at all — maxPromptsPerServer (200) clamps it to ~200 before
// the aggregate cap is ever evaluated, so `len(registered) <= 1002` held
// trivially regardless of whether the aggregate cap worked. Fixed by using
// SIX hidden servers at 200 prompts each (1200 upstream prompts total,
// comfortably over the 1000 cap even after every one individually clears
// the per-server cap) plus one authorized prompt on "a" — the aggregate cap
// can only be exercised by CROSSING the per-server ceiling with multiple
// servers, so this shape is the only one that actually proves it.
func TestScopeRetainedEffect_GlobalPromptCap(t *testing.T) {
	runRetainedEffectScenario(t, "global-prompt-cap", func(t *testing.T) (string, []string) {
		proxy, _ := createTestProxyWithRuntime(t, nil)
		sentinel := "SENTINEL_promptcap_7e3d"

		servers := map[string][]mcp.Prompt{
			"a": {{Name: "authorized_prompt", Description: "Authorized prompt"}},
		}
		const hiddenServers = 6
		const promptsPerHiddenServer = 200 // == maxPromptsPerServer; each server alone clears the per-server cap
		totalUpstreamAdvertised := 1       // "a"'s one authorized prompt
		for s := 0; s < hiddenServers; s++ {
			name := "hidden" + itoaPadded(s)
			prompts := make([]mcp.Prompt, 0, promptsPerHiddenServer)
			for i := 0; i < promptsPerHiddenServer; i++ {
				prompts = append(prompts, mcp.Prompt{Name: "p" + itoaPadded(s) + "_" + promptIndexName(i), Description: sentinel})
			}
			servers[name] = prompts
			totalUpstreamAdvertised += promptsPerHiddenServer
		}
		require.Greater(t, totalUpstreamAdvertised, 1000,
			"fixture: total upstream-advertised prompts must exceed maxAggregatedPrompts for this to test the aggregate cap at all")
		connectPromptUpstream(t, proxy, servers)

		registered := proxy.server.ListPrompts()
		const builtinPromptCount = 2 // setup-new-mcp-server, troubleshoot-mcp-server
		require.LessOrEqual(t, len(registered), 1000+builtinPromptCount,
			"the maxAggregatedPrompts backstop must still apply with %d upstream-advertised prompts present", totalUpstreamAdvertised)
		require.Less(t, len(registered), totalUpstreamAdvertised+builtinPromptCount,
			"the cap must actually have TRUNCATED something — %d advertised prompts must not all have been registered", totalUpstreamAdvertised)

		// Content check (cross-model review round 2: checking only NAMES was
		// vacuous — the sentinel lives exclusively in prompt DESCRIPTIONS,
		// which never appear in a bare name list regardless of correctness).
		// This effect has no scoped-vs-hidden CALLER distinction the way the
		// other retained effects do (the cap is a numeric ceiling applied
		// before any authorization check, not a disclosure path) — the
		// meaningful, non-vacuous claim is narrower: WHEN "a"'s own single
		// authorized prompt survives the cap (order-dependent, like every
		// other assertion in this file that depends on map iteration), an
		// "a"-only-scoped token fetching it through the REGISTERED handler
		// (which runs the real authorize() check, per
		// TestScopeRetainedEffect_PromptNameCollision's established pattern)
		// gets ITS OWN content, never a hidden server's sentinel-tagged one.
		var contentText strings.Builder
		const aPromptName = "a__authorized_prompt"
		if entry, ok := registered[aPromptName]; ok {
			aOnly := agentCtx([]string{"a"}, allPerms, "")
			result, err := entry.Handler(aOnly, mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: aPromptName}})
			require.NoError(t, err, "the a-only token must be able to fetch its OWN authorized prompt when it survives the cap")
			for _, msg := range result.Messages {
				if tc, ok := msg.Content.(mcp.TextContent); ok {
					contentText.WriteString(tc.Text)
				}
			}
			assert.NotContains(t, contentText.String(), sentinel,
				"a's own prompt content must never contain a hidden server's sentinel")
		}

		var names strings.Builder
		for name := range registered {
			names.WriteString(name)
			names.WriteString(" ")
		}
		return names.String() + " " + contentText.String(), []string{sentinel}
	})
}

func promptIndexName(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "p0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return "p" + string(b)
}

// ---------------------------------------------------------------------------
// 5. Direct display-name collision — fleet-wide collision admission on the
//    direct surface (a hidden server can withhold an authorized entry).
//    Already pinned, deterministically, by mcp_direct_catalog_test.go; this
//    re-runs that coverage as this suite's named fixture (parity with how
//    scope_differential_test.go re-registers per-PR tests by name).
// ---------------------------------------------------------------------------

func TestScopeRetainedEffect_DirectDisplayNameCollision(t *testing.T) {
	t.Run("direct-display-name-collision", func(t *testing.T) {
		TestBuildDirectCatalog_WithholdsCollidingDisplayNames(t)
	})
}
