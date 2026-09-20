package server

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 105 PR H1 — scope-regression-suite part 2 (FR-011/013/014, SC-007,
// gaps FR01x-G4..G7). This file is the two-fixture differential oracle
// (contracts/differential-oracle.md) plus the SC-007 coverage registry that
// ties every spec.md acceptance scenario to the test(s) that prove it.
//
// Design note (plan.md §4, "Two-fixture differential oracle"): PRs A-G each
// shipped standalone, rigorous tests against the Phase-1 fixtures for their
// own FR (scope_cache_fixtures_test.go, mcp_retrieve_scope_test.go,
// scope_target_tier_matrix_test.go, mcp_direct_publication_identity_test.go,
// mcp_direct_skew_test.go, mcp_direct_underscore_test.go,
// mcp_tail_log_scope_test.go, mcp_prompt_scope_test.go, profile_tool_test.go,
// profile_integration_test.go, mcp_auth_scope_test.go, ...). "H1 re-registers
// them by User Story id" (plan.md Delivery Structure, PR H1 row) — that is
// what reuseScopeCoverage below does: it runs the SAME already-verified test
// again as a named subtest of TestScopeCoverage_EveryUserStoryScenario, so a
// regression in any of them also fails the coverage gate, and records the
// US id -> test name mapping the inventory table below documents statically.
// newScopeFixture/runScopeScenario is the general two-fixture harness the
// contract also asks for; it is used directly for the scenarios that did not
// already have a dedicated differential fixture (US1.5's full oracle
// composition, US1.8's error-text identity, and the coverage self-test).
//
// ---------------------------------------------------------------------------
// SC-007 inventory table: US id -> proving test(s) -> `-run` pattern
// ---------------------------------------------------------------------------
//
//	US1.1  cached response redemption under broader auth (FR-001)
//	         TestScopeCacheFixture_PinnedTokenRESTDispatchAndRedemptionParity
//	         TestScopeCacheFixture_ProfiledAdminChildNotRedeemableByPinnedAgent
//	         TestScopeCacheFixture_RecursiveChildOnREST
//	         TestScopeCacheFixture_EmptyGrantAgentIsDenyAllOnRedemption
//	         TestScopeCacheFixture_HeldCallKeepsDispatchTimeSnapshot
//	         TestAuthorization_CallerKindFirst
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.1
//	US1.2  legacy/internal cache entries refused + durably invalidated (FR-002)
//	         TestScopeCacheFixture_UpgradeRecordRefusedAndAbsentAfterRestart
//	         TestScopeCacheFixture_FreshInternalEntryRefusedForEveryCaller
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.2
//	US1.3  set_profile effective intersection / non-selectable ≡ nonexistent (FR-003)
//	         TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers
//	         TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown
//	         TestHandleSetProfile_ScopedTokenDisjointProfilePresentVsAbsentIdentical
//	         TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles
//	         TestHandleSetProfile_PinnedZeroReachRefusedLikeDeletedPin
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.3
//	US1.4  profile URL deleted/missing/non-selectable pin, /mcp/p, /mcp/p/ (FR-004)
//	         TestProfile_PinnedTokenURLEnforcement
//	         TestProfile_DeletedPinDoesNotEnumerateProfiles
//	         TestProfile_ScopedUnpinnedRefusalUniform
//	         TestProfile_PinnedRefusalUniform
//	         TestProfile_PinnedRefusalIndependentOfFleet
//	         TestProfile_PinnedZeroReachURLRefusedUniformly
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.4
//	US1.5  retrieve_tools oracle: stats+debug+session_risk (FR-005)
//	         TestScopeDifferential_RetrieveToolsFullOracle (this file)
//	         TestRetrieveTools_ScopeOracle (mcp_retrieve_scope_test.go)
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.5
//	US1.6  aggregated prompts authorized by registration identity (FR-006)
//	         TestAggregatedPrompt_ScopeUsesCanonicalOwner
//	         TestUnstampedPrompt_WithheldFromListAndGet_ForAdminAndAgent
//	         TestAggregatedPrompt_LateEnableStillFiltered
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.6
//	US1.7  tail_log / per-server ops, a/b vs a_b, own-server attribution (FR-007)
//	         TestTailLog_CollidingLogFile_ScopedTokenGetsOnlyOwnRecords
//	         TestTailLog_CollidingLogFile_DifferentialWithHiddenCoOwner
//	         internal/logs.TestReadUpstreamServerLogTail_AttributedOnly_* (separate package; not re-invoked here)
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.7
//	US1.8  dispatch-denial / available-servers naming (FR-010 G1/G7)
//	         TestScopeDifferential_NoLiveClientAvailableServers (this file)
//	         TestHandleCallToolVariant_NoLiveClient_AvailableServersFilteredByScope
//	         TestHandleCallToolVariant_PinWiderThanToken_IndistinguishableFromNonexistent
//	         -run TestScopeCoverage_EveryUserStoryScenario/US1\.8
//	US2.1-US2.3  {read} token above tier / variant mismatch (FR-009 retrieve table)
//	         TestScopeTargetTier_RetrieveTable, TestScopeTargetTier_RetrieveTable_ReadDestructive
//	         -run TestScopeCoverage_EveryUserStoryScenario/US2\.1-3
//	US2.4  paired names erase (approved) vs ns:erase (denied/unapproved)
//	         TestScopeTargetTier_PairedNames_Retrieve, _Direct, _Nested
//	         -run TestScopeCoverage_EveryUserStoryScenario/US2\.4
//	US2.5  unresolved registration identity refused for every caller (D4)
//	         TestScopeTargetTier_UnresolvedIdentity
//	         -run TestScopeCoverage_EveryUserStoryScenario/US2\.5
//	US2.6  nested script call above tier, refusal envelope survives script success
//	         TestScopeTargetTier_NestedTable
//	         -run TestScopeCoverage_EveryUserStoryScenario/US2\.6
//	US3.1  origin-flip: every seam's listing carries its producing identity
//	         TestDirectPublication_OriginFlip_DefinitionMatchesProducingIdentity
//	         TestSkew_OriginFlipNeverSplitsScopeFromDispatch
//	         -run TestScopeCoverage_EveryUserStoryScenario/US3\.1
//	US3.2  unparseable names / built-ins positively identified
//	         TestDirectPublication_SeamAddition_StructurallyAwkwardNamesNeverListedOutOfScope
//	         TestDirectUnderscoreServer_SteadyState, TestDirectUnderscoreServer_SeamVariant
//	         TestSkew_AddedNameBeforeItsCatalogEntry
//	         -run TestScopeCoverage_EveryUserStoryScenario/US3\.2
//	US3.3  same-owner tier change read->destructive withheld once registered
//	         TestDirectPublication_TierChangeWithheldDuringSeam_DeferredMode
//	         TestSkew_ReadScopedTokenNeverHasADestructiveCallAdmitted
//	         -run TestScopeCoverage_EveryUserStoryScenario/US3\.3
//	US3.4  dispatch follows the handler registered at each seam; refusal replaced
//	         TestDirectPublication_InSeamCallEnvelopeMatchesUnregisteredName
//	         -run TestScopeCoverage_EveryUserStoryScenario/US3\.4
//
// Retained-effect scenarios (SC-001 exclusions, spec.md:114,161) are in
// scope_retained_effects_test.go, run via runRetainedEffectScenario. The
// grep guard proving none of the four pinned-reversal assertions FR01x-G7
// named (gap-map.md §7, §3) survived in their original form is
// TestScopePinnedReversalsStayInverted, below.

// ---------------------------------------------------------------------------
// Generic two-fixture harness
// ---------------------------------------------------------------------------

// scopeFixture is the {a, b, a__b} (full=true) or {a}-only (full=false)
// environment every registered scenario compares. Server "a" is the
// authorized content every scenario's caller may see; "b" and "a__b" (full
// fixture only) carry a sentinel string in their tool name and description
// that must never reach the a-only token's response.
type scopeFixture struct {
	proxy     *MCPProxyServer
	rt        *runtime.Runtime
	sentinels []string
	full      bool
}

// newScopeFixture builds the fixture (contracts/differential-oracle.md).
// full=true adds hidden servers "b" and "a__b", each with one tool carrying
// a distinctive sentinel in its name and description; full=false is server
// "a" alone, letter-for-letter identical to the full fixture's "a".
func newScopeFixture(t *testing.T, full bool) *scopeFixture {
	t.Helper()
	servers := []*config.ServerConfig{{Name: "a", Enabled: true}}
	if full {
		servers = append(servers,
			&config.ServerConfig{Name: "b", Enabled: true},
			&config.ServerConfig{Name: "a__b", Enabled: true},
		)
	}
	proxy, rt := createTestProxyWithRuntime(t, servers)
	f := &scopeFixture{proxy: proxy, rt: rt, full: full}

	startCountingUpstream(t, proxy, rt, "a",
		readSpec("read_thing"), writeSpec("write_thing"), destructiveSpec("destroy_thing"),
		readSpec("erase"), writeSpec("ns:erase"))

	if full {
		sentB := "SENTINEL_scopeB_6f1c9a"
		sentAB := "SENTINEL_scopeAB_2d9e41"
		f.sentinels = []string{sentB, sentAB}
		startCountingUpstream(t, proxy, rt, "b",
			toolSpec{Name: sentB + "_tool", Description: "Handles " + sentB,
				Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}})
		startCountingUpstream(t, proxy, rt, "a__b",
			toolSpec{Name: sentAB + "_tool", Description: "Handles " + sentAB,
				Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}})
		// Usage history on hidden tools (cross-model review round 1): US1.5's
		// retrieve oracle claim is meaningless if nothing in the fixture
		// actually differs on the fleet-wide path a leak would take —
		// usage_summary's top_tools is fleet-wide by construction
		// (mcp_retrieve_scope_test.go FR005-G2), so seeding real usage on a
		// hidden tool gives TestScopeDifferential_RetrieveToolsFullOracle an
		// actual discriminator: if the scope filter on usage ranking ever
		// regressed, "b"'s sentinel tool would outrank "a"'s and this
		// fixture's normalized(narrow) != normalized(full) would catch it.
		require.NoError(t, proxy.storage.IncrementToolUsage("b:"+sentB+"_tool"))
		require.NoError(t, proxy.storage.IncrementToolUsage("b:"+sentB+"_tool"))
		require.NoError(t, proxy.storage.IncrementToolUsage("b:"+sentB+"_tool"))
	}
	return f
}

// scopeCoverage records every US id proven by this test binary run, guarded
// by scopeCoverageMu since subtests may run in parallel.
var (
	scopeCoverageMu sync.Mutex
	scopeCoverage   = map[string]string{} // usID -> the *testing.T name that proved it
)

func registerScopeCoverage(t *testing.T, usID string) {
	t.Helper()
	scopeCoverageMu.Lock()
	defer scopeCoverageMu.Unlock()
	scopeCoverage[usID] = t.Name()
}

// scopeNormalizers strip only NONDETERMINISTIC fields (contracts doc:
// "strips only nondeterministic fields ... sorts unordered lists. Seeded
// usage counts are deterministic and are compared."): ISO-8601-ish
// timestamps, long hex ids/keys (cache keys, request ids), and durations.
var scopeNormalizers = []*regexp.Regexp{
	regexp.MustCompile(`"(?:timestamp|created_at|last_accessed|expires_at|started_at|completed_at|connected_at|last_seen)":"[^"]*"`),
	regexp.MustCompile(`\b[0-9a-fA-F]{32,64}\b`),
	regexp.MustCompile(`"request_id"\s*:\s*"[^"]*"`),
	regexp.MustCompile(`\b\d+(\.\d+)?(ms|µs|ns)\b`),
}

func normalizeScopeResponse(s string) string {
	for _, re := range scopeNormalizers {
		s = re.ReplaceAllString(s, "NORM")
	}
	return s
}

// runScopeScenario runs fn against the full and narrow fixtures with the SAME
// a-only token, asserts the normalised outputs are identical and that no
// sentinel leaked into the full fixture's raw response, and registers usID
// as covered.
func runScopeScenario(t *testing.T, usID string, fn func(t *testing.T, f *scopeFixture, ctx context.Context) string) {
	t.Helper()
	t.Run(usID, func(t *testing.T) {
		registerScopeCoverage(t, usID)
		full := newScopeFixture(t, true)
		narrow := newScopeFixture(t, false)
		mkCtx := func() context.Context { return agentCtx([]string{"a"}, allPerms, "") }

		outFull := fn(t, full, mkCtx())
		outNarrow := fn(t, narrow, mkCtx())

		require.Equal(t, normalizeScopeResponse(outNarrow), normalizeScopeResponse(outFull),
			"US %s: the a-only token's response must be identical between the {a,b,a__b} and {a}-only fixtures after normalisation", usID)
		for _, s := range full.sentinels {
			require.NotContains(t, outFull, s, "US %s: response must not contain sentinel %q", usID, s)
		}
	})
}

// reuseScopeCoverage registers usID as proven by an ALREADY-EXISTING,
// dedicated test elsewhere in this package (see the inventory table above)
// and re-runs it as a subtest, so a regression in that test also fails
// TestScopeCoverage_EveryUserStoryScenario. This is the re-registration
// plan.md's PR H1 row describes: "PRs A-G ship standalone tests on the
// Phase-1 fixtures; H1 re-registers them by User Story id."
func reuseScopeCoverage(t *testing.T, usID string, existing ...func(t *testing.T)) {
	t.Helper()
	reuseScopeCoverageMulti(t, []string{usID}, existing...)
}

// reuseScopeCoverageMulti is reuseScopeCoverage for the case where several US
// ids are proven by the SAME underlying test(s) (e.g. US2.1-US2.3 are all
// cells of the one generated 54-cell retrieve table) — the test runs once,
// every listed id is registered as covered by it.
func reuseScopeCoverageMulti(t *testing.T, usIDs []string, existing ...func(t *testing.T)) {
	t.Helper()
	t.Run(joinIDs(usIDs), func(t *testing.T) {
		for _, id := range usIDs {
			registerScopeCoverage(t, id)
		}
		for _, fn := range existing {
			fn(t)
		}
	})
}

func joinIDs(ids []string) string {
	out := ids[0]
	for _, id := range ids[1:] {
		out += "_" + id
	}
	return out
}

// ---------------------------------------------------------------------------
// US1.5 — retrieve_tools oracle (include_stats + debug + session_risk)
// ---------------------------------------------------------------------------

// TestScopeDifferential_RetrieveToolsFullOracle is the US1.5 acceptance
// scenario run through the general two-fixture harness: an a-only token
// calling retrieve_tools with include_stats, debug and session_risk all on
// must see byte-identical scope-independent fields (usage summary shape,
// indexed counts, session risk) whether or not b/a__b exist, and no
// sentinel. The ranking-dependent fields (tools, total, filter_diagnostics)
// are EXCLUDED here by construction: "a" carries exactly the same content in
// both fixtures and the query term is chosen to match only "a" tools, so the
// ranked window itself is expected to already be identical — this is the
// SC-001 "not displaced" case, not a substitute for the per-fixture oracle
// derivation TestRetrieveTools_ScopeOracle (mcp_retrieve_scope_test.go)
// performs with a genuinely displacing hidden tool.
func TestScopeDifferential_RetrieveToolsFullOracle(t *testing.T) {
	runScopeScenario(t, "US1.5", func(t *testing.T, f *scopeFixture, ctx context.Context) string {
		// Seed matching usage on "a"'s own tool so usage_summary.top_tools
		// has real, scope-independent content to compare (not just an empty
		// list both fixtures trivially share) — the hidden discriminator is
		// newScopeFixture's usage on "b"'s sentinel tool, which must never
		// surface here regardless of relative counts (mcp_retrieve_scope_test.go's
		// TestRetrieveTools_ScopeOracle is the dedicated FR005-G1..G5 oracle
		// derivation test for the ranking-displacement case; this one proves
		// the SAME invariant through the general two-fixture harness with a
		// non-ranking discriminator: usage_summary and session_risk).
		require.NoError(t, f.proxy.storage.IncrementToolUsage("a:read_thing"))

		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{
			"query": "read_thing", "limit": float64(10),
			"include_stats": true, "debug": true,
		}
		result, err := f.proxy.handleRetrieveTools(ctx, req)
		require.NoError(t, err)
		require.False(t, result.IsError, "retrieve_tools must not error for the a-only token: %v", result.Content)
		return stripRankingDependentFields(t, resultText(t, result))
	})
}

// stripRankingDependentFields removes the fields SC-001/the retrieve oracle
// (spec.md:114, contracts/differential-oracle.md) explicitly EXCLUDES from
// cross-fixture equality — the ranked window itself (`tools`, `total`),
// `filter_diagnostics`, and any per-tool `score` — before a scenario
// compares the rest byte-for-byte. Those fields are proven correct
// separately, per fixture, against the retrieve oracle's own derivation
// (TestRetrieveTools_ScopeOracle, mcp_retrieve_scope_test.go); comparing
// them here across fixtures would be WRONG per the contract even when this
// fixture's narrow query happens not to create a ranking difference — a
// hidden document is allowed to change a legitimate BM25 score.
func stripRankingDependentFields(t *testing.T, raw string) string {
	t.Helper()
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	delete(payload, "tools")
	delete(payload, "total")
	delete(payload, "filter_diagnostics")
	out, err := json.Marshal(payload)
	require.NoError(t, err)
	return string(out)
}

// ---------------------------------------------------------------------------
// US1.8 — dispatch denial / available-servers naming
// ---------------------------------------------------------------------------

// TestScopeDifferential_NoLiveClientAvailableServers is the US1.8 scenario:
// when the target server has no live client, the error text an a-only token
// receives must be identical whether or not hidden servers b/a__b exist in
// the fleet, and must never name them.
func TestScopeDifferential_NoLiveClientAvailableServers(t *testing.T) {
	runScopeScenario(t, "US1.8", func(t *testing.T, f *scopeFixture, ctx context.Context) string {
		// Disconnect "a"'s live client so the no-live-client branch fires,
		// without removing it from storage/StateView (the branch's
		// precondition: registered, no client).
		f.proxy.upstreamManager.RemoveServer("a")

		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"name": "a:read_thing", "args": map[string]interface{}{}}
		result, err := f.proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.True(t, result.IsError, "no live client must be refused")
		return resultText(t, result)
	})
}

// ---------------------------------------------------------------------------
// Coverage registry
// ---------------------------------------------------------------------------

// requiredScopeScenarios is every acceptance-scenario id spec.md names for
// User Stories 1-3 (US1.1-1.8, US2.1-2.6, US3.1-3.4).
func requiredScopeScenarios() []string {
	var ids []string
	for i := 1; i <= 8; i++ {
		ids = append(ids, "US1."+strconv.Itoa(i))
	}
	for i := 1; i <= 6; i++ {
		ids = append(ids, "US2."+strconv.Itoa(i))
	}
	for i := 1; i <= 4; i++ {
		ids = append(ids, "US3."+strconv.Itoa(i))
	}
	return ids
}

// TestScopeCoverage_EveryUserStoryScenario is H1's independent test
// (tasks.md Phase 10): it passes only when US1.1-1.8, US2.1-2.6, US3.1-3.4
// are all registered — each as a subtest that either runs the general
// two-fixture harness directly or re-runs the dedicated per-PR differential
// test(s) the inventory table above names. A coverage gap (a US id nothing
// registers) or a regression in any re-run test fails this test.
func TestScopeCoverage_EveryUserStoryScenario(t *testing.T) {
	// Reset the package-global registry first (cross-model review round 1):
	// this test must prove coverage from its OWN registrations, never rely on
	// another top-level test (e.g. TestScopeDifferential_RetrieveToolsFullOracle,
	// run earlier in source order) having already populated an entry this
	// test never registers itself. `go test -run TestScopeCoverage_EveryUserStoryScenario`
	// alone always started from an empty map anyway (a fresh process); this
	// guards the full-package run too.
	scopeCoverageMu.Lock()
	scopeCoverage = map[string]string{}
	scopeCoverageMu.Unlock()

	// US1 — a server-restricted token cannot learn about other servers.
	reuseScopeCoverage(t, "US1.1",
		TestScopeCacheFixture_PinnedTokenRESTDispatchAndRedemptionParity,
		TestScopeCacheFixture_ProfiledAdminChildNotRedeemableByPinnedAgent,
		TestScopeCacheFixture_RecursiveChildOnREST,
		TestScopeCacheFixture_EmptyGrantAgentIsDenyAllOnRedemption,
		TestScopeCacheFixture_HeldCallKeepsDispatchTimeSnapshot,
		// internal/cache.TestAuthorization_CallerKindFirst also proves this
		// scenario's kind-first rule, but lives in a different Go package
		// (internal/cache) and so cannot be re-invoked from here; it is
		// exercised by its own package's `go test ./internal/cache/...` run.
	)
	reuseScopeCoverage(t, "US1.2",
		TestScopeCacheFixture_UpgradeRecordRefusedAndAbsentAfterRestart,
		TestScopeCacheFixture_FreshInternalEntryRefusedForEveryCaller,
	)
	reuseScopeCoverage(t, "US1.3",
		TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers,
		TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown,
		TestHandleSetProfile_ScopedTokenDisjointProfilePresentVsAbsentIdentical,
		TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles,
		TestHandleSetProfile_PinnedZeroReachRefusedLikeDeletedPin,
	)
	reuseScopeCoverage(t, "US1.4",
		TestProfile_PinnedTokenURLEnforcement,
		TestProfile_DeletedPinDoesNotEnumerateProfiles,
		TestProfile_ScopedUnpinnedRefusalUniform,
		TestProfile_PinnedRefusalUniform,
		TestProfile_PinnedRefusalIndependentOfFleet,
		TestProfile_PinnedZeroReachURLRefusedUniformly,
	)
	reuseScopeCoverage(t, "US1.5",
		TestScopeDifferential_RetrieveToolsFullOracle,
		TestRetrieveTools_ScopeOracle,
	)
	reuseScopeCoverage(t, "US1.6",
		TestAggregatedPrompt_ScopeUsesCanonicalOwner,
		TestUnstampedPrompt_WithheldFromListAndGet_ForAdminAndAgent,
		TestAggregatedPrompt_LateEnableStillFiltered,
	)
	reuseScopeCoverage(t, "US1.7",
		TestTailLog_CollidingLogFile_ScopedTokenGetsOnlyOwnRecords,
		TestTailLog_CollidingLogFile_DifferentialWithHiddenCoOwner,
	)
	reuseScopeCoverage(t, "US1.8",
		TestScopeDifferential_NoLiveClientAvailableServers,
		TestHandleCallToolVariant_NoLiveClient_AvailableServersFilteredByScope,
		TestHandleCallToolVariant_PinWiderThanToken_IndistinguishableFromNonexistent,
	)

	// US2 — a read-only token cannot execute above its tier. US2.1-2.3 are
	// all cells of the one generated 54-cell retrieve table (T009); it runs
	// once and proves all three.
	reuseScopeCoverageMulti(t, []string{"US2.1", "US2.2", "US2.3"}, TestScopeTargetTier_RetrieveTable)
	reuseScopeCoverage(t, "US2.4",
		TestScopeTargetTier_PairedNames_Retrieve,
		TestScopeTargetTier_PairedNames_Direct,
		TestScopeTargetTier_PairedNames_Nested,
	)
	reuseScopeCoverage(t, "US2.5", TestScopeTargetTier_UnresolvedIdentity)
	reuseScopeCoverage(t, "US2.6", TestScopeTargetTier_NestedTable)

	// US3 — a listing never returns a definition its own publication would
	// not authorize.
	reuseScopeCoverage(t, "US3.1",
		TestDirectPublication_OriginFlip_DefinitionMatchesProducingIdentity,
		TestSkew_OriginFlipNeverSplitsScopeFromDispatch,
	)
	reuseScopeCoverage(t, "US3.2",
		TestDirectPublication_SeamAddition_StructurallyAwkwardNamesNeverListedOutOfScope,
		TestDirectUnderscoreServer_SteadyState,
		TestDirectUnderscoreServer_SeamVariant,
		TestSkew_AddedNameBeforeItsCatalogEntry,
	)
	reuseScopeCoverage(t, "US3.3",
		TestDirectPublication_TierChangeWithheldDuringSeam_DeferredMode,
		TestSkew_ReadScopedTokenNeverHasADestructiveCallAdmitted,
	)
	reuseScopeCoverage(t, "US3.4",
		TestDirectPublication_InSeamCallEnvelopeMatchesUnregisteredName,
	)

	scopeCoverageMu.Lock()
	missing := make([]string, 0)
	for _, id := range requiredScopeScenarios() {
		if _, ok := scopeCoverage[id]; !ok {
			missing = append(missing, id)
		}
	}
	scopeCoverageMu.Unlock()
	sort.Strings(missing)
	require.Empty(t, missing, "coverage gap: the following User Story scenarios have no registered proving test: %v", missing)
}
