package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Issue #969 (Phase 0) — preflight baseline counters, server side.

// pinTelemetryEnvEnabledForServer neutralises the env opt-outs so these tests
// exercise the ENABLED path even on a CI machine (CI=true is a telemetry
// opt-out, which would silently turn every increment into a no-op).
func pinTelemetryEnvEnabledForServer(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
}

// --- follow-through bookkeeping (pure, no runtime needed) ---

func TestActiveFilterKeys(t *testing.T) {
	require.Empty(t, activeFilterKeys(false, false, false))
	require.Equal(t, []string{filterKeyReadOnlyOnly}, activeFilterKeys(true, false, false))
	require.Equal(t,
		[]string{filterKeyReadOnlyOnly, filterKeyExcludeDestruct, filterKeyExcludeOpenWorld},
		activeFilterKeys(true, true, true))
}

func diagBlaming(filters ...string) *filterDiagnostics {
	d := &filterDiagnostics{OmittedByFilter: map[string]reasonCounts{}}
	for _, f := range filters {
		d.OmittedByFilter[f] = reasonCounts{MissingAnnotation: 1}
		d.OmittedTotal++
	}
	return d
}

// A follow-up that DROPS the blamed filter counts as followed.
func TestFilterDiagFollowUp_DroppedFilterCounts(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), now)
	require.True(t, p.consumeFilterDiagFollowUp("sess-1", nil, now.Add(5*time.Second)))
}

// A follow-up that RELAXES one of several blamed filters still counts.
func TestFilterDiagFollowUp_RelaxedSubsetCounts(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1",
		diagBlaming(filterKeyReadOnlyOnly, filterKeyExcludeOpenWorld), now)
	// exclude_open_world dropped, read_only_only kept.
	require.True(t, p.consumeFilterDiagFollowUp("sess-1",
		activeFilterKeys(true, false, false), now.Add(time.Second)))
}

// Re-running the SAME filters is not a follow-up.
func TestFilterDiagFollowUp_SameFiltersDoNotCount(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), now)
	require.False(t, p.consumeFilterDiagFollowUp("sess-1",
		activeFilterKeys(true, false, false), now.Add(time.Second)))
}

// The note is consumed once: a second relaxed call cannot double-count the
// same diagnostics block.
func TestFilterDiagFollowUp_NoteConsumedOnce(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), now)
	require.True(t, p.consumeFilterDiagFollowUp("sess-1", nil, now.Add(time.Second)))
	require.False(t, p.consumeFilterDiagFollowUp("sess-1", nil, now.Add(2*time.Second)))
}

// Follow-ups from a DIFFERENT session never match.
func TestFilterDiagFollowUp_SessionScoped(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), now)
	require.False(t, p.consumeFilterDiagFollowUp("sess-2", nil, now.Add(time.Second)))
	// sess-1's note is untouched by sess-2's call.
	require.True(t, p.consumeFilterDiagFollowUp("sess-1", nil, now.Add(2*time.Second)))
}

// Sessions without an id are never pooled together under "".
func TestFilterDiagFollowUp_AnonymousSessionsIgnored(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("", diagBlaming(filterKeyReadOnlyOnly), now)
	require.Empty(t, p.filterDiagNotes)
	require.False(t, p.consumeFilterDiagFollowUp("", nil, now))
}

// A stale note expires rather than crediting an unrelated later task.
func TestFilterDiagFollowUp_TTLExpiry(t *testing.T) {
	p := &MCPProxyServer{}
	now := time.Now()

	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), now)
	require.False(t, p.consumeFilterDiagFollowUp("sess-1", nil, now.Add(filterDiagNoteTTL+time.Minute)))
}

// The note map is bounded: a proxy serving many sessions must not grow it
// without limit for a telemetry counter.
func TestFilterDiagNotes_Bounded(t *testing.T) {
	p := &MCPProxyServer{}
	base := time.Now()

	for i := 0; i < maxFilterDiagNotes*3; i++ {
		sid := "sess-" + time.Duration(i).String()
		p.noteFilterDiagnostics(sid, diagBlaming(filterKeyReadOnlyOnly), base.Add(time.Duration(i)*time.Millisecond))
	}
	require.LessOrEqual(t, len(p.filterDiagNotes), maxFilterDiagNotes)
}

// Concurrent retrieve_tools calls in ONE session can complete out of order.
// The note the agent most recently received must win, so a LATE-finishing
// earlier call may not clobber it (opencode review, round 1, finding 3).
func TestFilterDiagNotes_StaleWriteDoesNotClobberNewer(t *testing.T) {
	p := &MCPProxyServer{}
	base := time.Now()

	// Call B (started later) lands first, then call A (started earlier) lands.
	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyExcludeDestruct), base.Add(time.Second))
	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), base)

	p.filterDiagMu.Lock()
	note := p.filterDiagNotes["sess-1"]
	p.filterDiagMu.Unlock()
	require.Equal(t, []string{filterKeyExcludeDestruct}, note.filters,
		"the newer note must survive the late-landing older write")
	require.True(t, note.at.Equal(base.Add(time.Second)),
		"the newer timestamp must survive, else the TTL expires early")

	// The follow-up is therefore judged against the filters the agent actually
	// saw last: dropping exclude_destructive counts.
	require.True(t, p.consumeFilterDiagFollowUp("sess-1",
		[]string{filterKeyReadOnlyOnly}, base.Add(2*time.Second)))
}

// Refreshing a session that ALREADY has a note cannot grow the map, so it must
// not evict an unrelated session's still-eligible note (finding 4).
func TestFilterDiagNotes_RefreshAtCapacityEvictsNothing(t *testing.T) {
	p := &MCPProxyServer{}
	base := time.Now()

	// Fill exactly to capacity; sess-0 is the oldest and therefore the entry a
	// capacity eviction would take.
	for i := 0; i < maxFilterDiagNotes; i++ {
		p.noteFilterDiagnostics(fmt.Sprintf("sess-%d", i),
			diagBlaming(filterKeyReadOnlyOnly), base.Add(time.Duration(i)*time.Millisecond))
	}
	require.Len(t, p.filterDiagNotes, maxFilterDiagNotes)

	// The NEWEST session gets a second diagnostics block. This replaces its own
	// key, so nothing needs to be evicted.
	newest := fmt.Sprintf("sess-%d", maxFilterDiagNotes-1)
	p.noteFilterDiagnostics(newest, diagBlaming(filterKeyExcludeDestruct), base.Add(time.Hour))

	require.Len(t, p.filterDiagNotes, maxFilterDiagNotes)
	p.filterDiagMu.Lock()
	_, oldestSurvived := p.filterDiagNotes["sess-0"]
	p.filterDiagMu.Unlock()
	require.True(t, oldestSurvived,
		"replacing an existing key must not cost an unrelated session its note")
}

// The consume side has the same out-of-order hazard as the write side: a call
// that STARTED before the note was written cannot be a reaction to it, so it
// must neither count itself as a follow-up nor destroy the note the genuinely
// later call still needs (opencode review, round 2, finding 2).
func TestFilterDiagFollowUp_EarlierCallCannotConsumeNewerNote(t *testing.T) {
	p := &MCPProxyServer{}
	base := time.Now()

	// Call B is handed the block at base+1s.
	p.noteFilterDiagnostics("sess-1", diagBlaming(filterKeyReadOnlyOnly), base.Add(time.Second))

	// Call A started at base — before that block existed — and drops the
	// blamed filter for unrelated reasons. It must not count.
	require.False(t, p.consumeFilterDiagFollowUp("sess-1", nil, base),
		"a call that predates the block cannot be a reaction to it")

	// And the note must still be there for the call that genuinely follows.
	require.True(t, p.consumeFilterDiagFollowUp("sess-1", nil, base.Add(2*time.Second)),
		"the stale call must not have consumed the note")
}

// A block that tool_response_limit cut back out of the payload was never
// received, so it is neither an emission nor something a later call can follow
// (opencode review, round 2, finding 3).
func TestFilterDiagnosticsSurvived(t *testing.T) {
	withBlock := `{"filter_diagnostics":{"omitted_total":3},"tools":[]}`
	cutAway := `{"tools":[{"name":"a"}]}... [truncated]`

	// Untruncated responses always carry what was attached — no scan needed.
	require.True(t, filterDiagnosticsSurvived(cutAway, false),
		"an untruncated response carries the block the handler attached")

	require.True(t, filterDiagnosticsSurvived(withBlock, true),
		"truncation that preserved the block still counts as emitted")
	require.False(t, filterDiagnosticsSurvived(cutAway, true),
		"a block truncated out of the payload was never delivered")
}

// --- direct-mode block reason classification (finding 5) ---

// Direct mode funnels every callability block through ONE emit site. The reason
// key must still come from what actually fired, or the whole availability
// reason distribution collapses into tool_not_callable.
func TestDirectBlockReasonKey_ClassifiesPerGate(t *testing.T) {
	quarantined := directCallabilityDecision{
		serverConfig: &config.ServerConfig{Name: "github", Enabled: true, Quarantined: true},
	}
	require.Equal(t, telemetry.BlockReasonServerQuarantined, directBlockReasonKey(quarantined))

	pending := directCallabilityDecision{
		serverConfig:   &config.ServerConfig{Name: "github", Enabled: true},
		approvalStatus: storage.ToolApprovalStatusPending,
	}
	require.Equal(t, telemetry.BlockReasonToolPendingApproval, directBlockReasonKey(pending))

	changed := directCallabilityDecision{
		serverConfig:   &config.ServerConfig{Name: "github", Enabled: true},
		approvalStatus: storage.ToolApprovalStatusChanged,
	}
	require.Equal(t, telemetry.BlockReasonToolChanged, directBlockReasonKey(changed))

	disabled := directCallabilityDecision{
		serverConfig: &config.ServerConfig{Name: "github", Enabled: false},
	}
	require.Equal(t, telemetry.BlockReasonToolNotCallable, directBlockReasonKey(disabled))

	// Every key the classifier can produce is a member of the closed enum, so a
	// direct-mode block can never land in the "other" overflow bucket.
	for _, d := range []directCallabilityDecision{quarantined, pending, changed, disabled} {
		require.True(t, telemetry.IsAvailabilityBlockReason(directBlockReasonKey(d)))
	}
}

// The reason travels with the block result, so the routing handler emits the
// key that matches the gate that fired.
func TestDirectToolCallabilityBlockWithReason_Quarantine(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true, Quarantined: true,
	}))

	result, reasonKey := proxy.directToolCallabilityBlockWithReason(
		context.Background(), "github", "list_repos", map[string]interface{}{})
	require.NotNil(t, result)
	require.Equal(t, telemetry.BlockReasonServerQuarantined, reasonKey)
}

// A callable tool yields no block and no reason key.
func TestDirectToolCallabilityBlockWithReason_CallableIsSilent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
	}))

	result, reasonKey := proxy.directToolCallabilityBlockWithReason(
		context.Background(), "github", "list_repos", map[string]interface{}{})
	require.Nil(t, result)
	require.Empty(t, reasonKey)
}

// --- end-to-end through retrieve_tools ---

// newPreflightCountedProxy builds the spec-094 fixture proxy with a telemetry
// service (and therefore the preflight counter store) wired onto the runtime's
// BBolt DB, so the counters the handler bumps can be read back.
func newPreflightCountedProxy(t *testing.T) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()
	pinTelemetryEnvEnabledForServer(t)

	proxy, rt := newFilterDiagnosticsProxy(t)
	rt.SetTelemetry("v1.0.0", "personal")
	ts := rt.TelemetryService()
	require.NotNil(t, ts, "telemetry service must be wired")
	require.NotNil(t, ts.PreflightCounterStore(), "preflight counter store must be wired")
	return proxy, rt
}

func preflightSnapshot(t *testing.T, rt *runtime.Runtime) telemetry.PreflightCounters {
	t.Helper()
	ts := rt.TelemetryService()
	require.NotNil(t, ts)
	snap, err := ts.PreflightCounterStore().Snapshot(ts.PreflightCounterDB())
	require.NoError(t, err)
	return snap
}

func retrieveWithSession(t *testing.T, proxy *MCPProxyServer, sessionID string, args map[string]interface{}) {
	t.Helper()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	_, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
}

// A response that carries a filter_diagnostics block bumps the emitted counter
// and both reason-class sums.
func TestPreflightCounters_FilterDiagnosticsEmitted(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
		"query":          diagQueryAll,
		"read_only_only": true,
	})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 1, snap.FilterDiagEmitted24h)
	require.Positive(t, snap.FilterDiagMissingAnnotation24h,
		"the fixture omits unannotated tools, so the missing class must be non-zero")
	require.Positive(t, snap.FilterDiagExplicit24h,
		"the fixture omits an explicitly non-read-only tool")
	require.Equal(t, 0, snap.FilterDiagFollowed24h)
}

// The happy path (no filters) attaches no block and therefore counts nothing.
func TestPreflightCounters_NoDiagnosticsNoCount(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{"query": diagQueryAll})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 0, snap.FilterDiagEmitted24h)
	require.Equal(t, 0, snap.FilterDiagFollowed24h)
}

// A second call in the SAME session that drops the blamed filter is counted as
// a follow-through; a same-session repeat of the same filters is not.
func TestPreflightCounters_FilterDiagnosticsFollowed(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
		"query":          diagQueryAll,
		"read_only_only": true,
	})
	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
		"query": diagQueryAll,
	})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 1, snap.FilterDiagEmitted24h)
	require.Equal(t, 1, snap.FilterDiagFollowed24h)
}

func TestPreflightCounters_FilterDiagnosticsNotFollowedWhenFiltersRepeat(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	for i := 0; i < 3; i++ {
		retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
			"query":          diagQueryAll,
			"read_only_only": true,
		})
	}

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 3, snap.FilterDiagEmitted24h)
	require.Equal(t, 0, snap.FilterDiagFollowed24h,
		"re-running the same filters is not a follow-through")
}

// A different session's later call must not be credited as a follow-through.
func TestPreflightCounters_FollowThroughIsSessionScoped(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
		"query":          diagQueryAll,
		"read_only_only": true,
	})
	retrieveWithSession(t, proxy, "sess-2", map[string]interface{}{
		"query": diagQueryAll,
	})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 0, snap.FilterDiagFollowed24h)
}

// A retrieve_tools response that withheld locked matches bumps the
// silent-unavailability counter; one that withheld nothing does not.
func TestPreflightCounters_DiscoveryOmission(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	// Disable a server so its indexed tools become locked (not callable) and
	// are dropped from the default (include_disabled=false) response.
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "plain", Enabled: false,
	}))

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{"query": diagQueryAll})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 1, snap.DiscoveryOmission24h)
}

func TestPreflightCounters_NoDiscoveryOmissionWhenNothingWithheld(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{"query": diagQueryAll})

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 0, snap.DiscoveryOmission24h)
}

// --- availability blocks ---

// A policy BLOCK increments the total and its structured reason bucket; a
// non-block decision (warn / redact) on the same funnel does not.
func TestPreflightCounters_AvailabilityBlockByReason(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	proxy.emitActivityPolicyDecision("srv", "tool", "sess-1", "req-1",
		"blocked", "Server is quarantined for security review",
		telemetry.BlockReasonServerQuarantined)
	proxy.emitActivityPolicyDecision("srv", "tool", "sess-1", "req-2",
		"blocked", "Server 'srv' is not in scope for this agent token",
		telemetry.BlockReasonTokenScope)
	proxy.emitActivityPolicyDecision("srv", "tool", "sess-1", "req-3",
		"redacted", "1 secret redacted", telemetry.BlockReasonOutputSanitisation)

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 2, snap.AvailabilityBlock24h, "only blocks count")
	require.Equal(t, 1, snap.AvailabilityBlockReasons24h[telemetry.BlockReasonServerQuarantined])
	require.Equal(t, 1, snap.AvailabilityBlockReasons24h[telemetry.BlockReasonTokenScope])
	require.Zero(t, snap.AvailabilityBlockReasons24h[telemetry.BlockReasonOutputSanitisation])
}

// The operator-facing prose (which embeds server and tool names) never becomes
// a counter key — an unclassified site lands in "other".
func TestPreflightCounters_UnclassifiedBlockFoldsIntoOther(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)

	proxy.emitActivityPolicyDecision("acme-internal", "purge_all", "sess-1", "req-1",
		"blocked", "Server 'acme-internal' is not in scope for this agent token",
		"some-future-unregistered-key")

	snap := preflightSnapshot(t, rt)
	require.Equal(t, 1, snap.AvailabilityBlock24h)
	require.Equal(t, 1, snap.AvailabilityBlockReasons24h[telemetry.BlockReasonOther])
	for key := range snap.AvailabilityBlockReasons24h {
		require.True(t, telemetry.IsAvailabilityBlockReason(key),
			"non-enum key %q reached the counters", key)
	}
}

// Every counter path is a no-op when telemetry is opted out at event time.
func TestPreflightCounters_OptOutRecordsNothing(t *testing.T) {
	proxy, rt := newPreflightCountedProxy(t)
	t.Setenv("DO_NOT_TRACK", "1")

	retrieveWithSession(t, proxy, "sess-1", map[string]interface{}{
		"query":          diagQueryAll,
		"read_only_only": true,
	})
	proxy.emitActivityPolicyDecision("srv", "tool", "sess-1", "req-1",
		"blocked", "quarantined", telemetry.BlockReasonServerQuarantined)

	snap := preflightSnapshot(t, rt)
	require.Equal(t, telemetry.PreflightCounters{}, snap,
		"nothing may be persisted while telemetry is opted out")
}

// The hooks must be safe on a proxy with no runtime at all (the CLI's
// in-process server), and on a runtime whose telemetry service was never set.
func TestPreflightCounters_NilSafe(t *testing.T) {
	bare := &MCPProxyServer{}
	bare.recordFilterDiagnosticsEmitted(diagBlaming(filterKeyReadOnlyOnly))
	bare.recordFilterDiagnosticsFollowed()
	bare.recordDiscoveryOmission()
	bare.recordAvailabilityBlock(telemetry.BlockReasonOther)

	proxy, _ := newFilterDiagnosticsProxy(t) // runtime, but no telemetry service
	proxy.recordFilterDiagnosticsEmitted(diagBlaming(filterKeyReadOnlyOnly))
	proxy.recordFilterDiagnosticsFollowed()
	proxy.recordDiscoveryOmission()
	proxy.recordAvailabilityBlock(telemetry.BlockReasonOther)
}
