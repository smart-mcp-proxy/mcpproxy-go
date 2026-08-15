package preflight

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func evalOne(t *testing.T, w *world, ref ToolRef) Result {
	t.Helper()
	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{ref})
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0]
}

func TestEvaluate_Ready(t *testing.T) {
	res := evalOne(t, healthyWorld(), ToolRef{ID: id})

	assert.Equal(t, id, res.ID)
	assert.Equal(t, StatusReady, res.Status)
	assert.Empty(t, res.Reason, "a ready result carries no failure reason")
	assert.False(t, res.Retryable)
	assert.Empty(t, res.Action)
	assert.Empty(t, res.Detail)
	assert.Empty(t, res.Remediation)
	assert.Empty(t, res.DidYouMean)
	assert.Equal(t, "sha256/v2:abc123", res.Hash, "operator tier gets the current pin on ready results")
}

// --- every enum cell --------------------------------------------------------

func TestEvaluate_EveryReasonCell(t *testing.T) {
	tests := []struct {
		name   string
		build  func() *world
		ref    ToolRef
		reason Reason
	}{
		{
			name:   "server_not_configured",
			build:  func() *world { return healthyWorld().unconfigure() },
			ref:    ToolRef{ID: id},
			reason: ReasonServerNotConfigured,
		},
		{
			name:   "server_not_in_scope (operator tier)",
			build:  func() *world { return healthyWorld().outOfScope() },
			ref:    ToolRef{ID: id},
			reason: ReasonServerNotInScope,
		},
		{
			name:   "server_quarantined",
			build:  func() *world { return healthyWorld().quarantine() },
			ref:    ToolRef{ID: id},
			reason: ReasonServerQuarantined,
		},
		{
			name:   "server_disabled",
			build:  func() *world { return healthyWorld().disable() },
			ref:    ToolRef{ID: id},
			reason: ReasonServerDisabled,
		},
		{
			name:   "not_found",
			build:  func() *world { return healthyWorld().unindex().forget() },
			ref:    ToolRef{ID: id},
			reason: ReasonNotFound,
		},
		{
			name:   "tool_denied_by_config",
			build:  func() *world { return healthyWorld().denyByConfig() },
			ref:    ToolRef{ID: id},
			reason: ReasonToolDeniedByConfig,
		},
		{
			name:   "tool_blocked_by_user",
			build:  func() *world { return healthyWorld().approval(func(a *ApprovalState) { a.Disabled = true }) },
			ref:    ToolRef{ID: id},
			reason: ReasonToolBlockedByUser,
		},
		{
			name: "tool_changed",
			build: func() *world {
				return healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusChanged })
			},
			ref:    ToolRef{ID: id},
			reason: ReasonToolChanged,
		},
		{
			name: "tool_pending_approval",
			build: func() *world {
				return healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending })
			},
			ref:    ToolRef{ID: id},
			reason: ReasonToolPendingApproval,
		},
		{
			name:   "hash_mismatch",
			build:  healthyWorld,
			ref:    ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"},
			reason: ReasonHashMismatch,
		},
		{
			name:   "oauth_required",
			build:  func() *world { return healthyWorld().runtime(RuntimeStatePendingAuth) },
			ref:    ToolRef{ID: id},
			reason: ReasonOAuthRequired,
		},
		{
			name:   "server_unhealthy (error)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateError) },
			ref:    ToolRef{ID: id},
			reason: ReasonServerUnhealthy,
		},
		{
			name:   "server_unhealthy (disconnected)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateDisconnected) },
			ref:    ToolRef{ID: id},
			reason: ReasonServerUnhealthy,
		},
		{
			name:   "server_initializing (connecting)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateConnecting) },
			ref:    ToolRef{ID: id},
			reason: ReasonServerInitializing,
		},
		{
			name:   "server_initializing (discovering)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateDiscovering) },
			ref:    ToolRef{ID: id},
			reason: ReasonServerInitializing,
		},
		{
			name:   "server_initializing (authenticating)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateAuthenticating) },
			ref:    ToolRef{ID: id},
			reason: ReasonServerInitializing,
		},
		{
			name: "missing_annotation",
			build: func() *world {
				w := healthyWorld().annotations(nil)
				w.filters.readOnlyOnly = true
				return w
			},
			ref:    ToolRef{ID: id},
			reason: ReasonMissingAnnotation,
		},
		{
			name: "policy_filtered",
			build: func() *world {
				w := healthyWorld().annotations(&config.ToolAnnotations{ReadOnlyHint: boolPtr(false)})
				w.filters.readOnlyOnly = true
				return w
			},
			ref:    ToolRef{ID: id},
			reason: ReasonPolicyFiltered,
		},
	}

	seen := map[Reason]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := evalOne(t, tt.build(), tt.ref)
			assert.Equal(t, StatusUnavailable, res.Status)
			assert.Equal(t, tt.reason, res.Reason)
			// Every failure result restates the taxonomy row.
			assert.Equal(t, Retryable(tt.reason), res.Retryable)
			assert.Equal(t, DefaultAction(tt.reason), res.Action)
			assert.Equal(t, DefaultRemediation(tt.reason), res.Remediation)
			assert.NotEmpty(t, res.Detail, "a failure always says something occurrence-specific")
			assert.Empty(t, res.Hash, "hashes are never disclosed on a failure result")
		})
		seen[tt.reason] = true
	}

	// FR-016 in miniature: no enum code may ship without a cell here.
	for _, code := range AllReasons() {
		assert.True(t, seen[code], "reason %s has no evaluator test cell", code)
	}
}

// --- adjacent-precedence co-occurrence (FR-004) -----------------------------

// Each case makes TWO adjacent precedence conditions true at once and asserts
// the higher-ranked one wins. Where two conditions cannot physically co-occur
// (a server has exactly one connection state; an approval record exactly one
// status), the test instead pins the discrimination that the pair encodes.
func TestEvaluate_PrecedenceCoOccurrence(t *testing.T) {
	tests := []struct {
		name   string
		build  func() *world
		ref    ToolRef
		want   Reason
		reason string
	}{
		{
			name:   "not_configured beats not_in_scope",
			build:  func() *world { return healthyWorld().unconfigure().outOfScope() },
			ref:    ToolRef{ID: id},
			want:   ReasonServerNotConfigured,
			reason: "a server that does not exist cannot be 'out of scope'",
		},
		{
			name:   "not_in_scope beats quarantined",
			build:  func() *world { return healthyWorld().quarantine().outOfScope() },
			ref:    ToolRef{ID: id},
			want:   ReasonServerNotInScope,
			reason: "scope is evaluated before any state of a server the caller cannot see",
		},
		{
			name:   "quarantined beats disabled",
			build:  func() *world { return healthyWorld().quarantine().disable() },
			ref:    ToolRef{ID: id},
			want:   ReasonServerQuarantined,
			reason: "quarantine is the security story and outranks the admin toggle",
		},
		{
			name:   "quarantined beats not_found",
			build:  func() *world { return healthyWorld().quarantine().unindex() },
			ref:    ToolRef{ID: id},
			want:   ReasonServerQuarantined,
			reason: "quarantined servers' tools are never indexed, so existence is unknowable",
		},
		{
			name:   "disabled beats not_found",
			build:  func() *world { return healthyWorld().disable().unindex() },
			ref:    ToolRef{ID: id},
			want:   ReasonServerDisabled,
			reason: "a disabled server's index may be stale; the actionable fact is the disable",
		},
		{
			name:   "not_found beats denied_by_config",
			build:  func() *world { return healthyWorld().unindex().forget().denyByConfig() },
			ref:    ToolRef{ID: id},
			want:   ReasonNotFound,
			reason: "a truly unknown tool (no index entry, no approval record) outranks a policy about it",
		},
		{
			name:   "de-indexed blocked tool keeps tool_blocked_by_user",
			build:  func() *world { return healthyWorld().unindex().approval(func(a *ApprovalState) { a.Disabled = true }) },
			ref:    ToolRef{ID: id},
			want:   ReasonToolBlockedByUser,
			reason: "the runtime de-indexes blocked tools; the approval record proves existence, so not_found must not shadow the real reason",
		},
		{
			name: "de-indexed changed tool keeps tool_changed",
			build: func() *world {
				return healthyWorld().unindex().approval(func(a *ApprovalState) { a.Status = ApprovalStatusChanged })
			},
			ref:    ToolRef{ID: id},
			want:   ReasonToolChanged,
			reason: "a rug-pulled tool is de-indexed; reporting not_found (plus a cross-server did_you_mean) would be actively misleading",
		},
		{
			name: "de-indexed pending tool keeps tool_pending_approval",
			build: func() *world {
				return healthyWorld().unindex().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending })
			},
			ref:    ToolRef{ID: id},
			want:   ReasonToolPendingApproval,
			reason: "a post-baseline new tool is held out of the index until approved; the record still proves it exists",
		},
		{
			name:   "unknown id on an initializing server is server_initializing, not not_found",
			build:  func() *world { return healthyWorld().unindex().forget().runtime(RuntimeStateDiscovering) },
			ref:    ToolRef{ID: id},
			want:   ReasonServerInitializing,
			reason: "existence is unknowable mid-discovery (FR-005); not_found requires an authoritative Ready view",
		},
		{
			name:   "unknown id on an unhealthy server is server_unhealthy, not not_found",
			build:  func() *world { return healthyWorld().unindex().forget().runtime(RuntimeStateError) },
			ref:    ToolRef{ID: id},
			want:   ReasonServerUnhealthy,
			reason: "a dead server cannot vouch for what does not exist on it",
		},
		{
			name: "denied_by_config beats blocked_by_user",
			build: func() *world {
				return healthyWorld().denyByConfig().approval(func(a *ApprovalState) { a.Disabled = true })
			},
			ref:    ToolRef{ID: id},
			want:   ReasonToolDeniedByConfig,
			reason: "operator config is not user-overridable, so it is the actionable lock",
		},
		{
			name: "blocked_by_user beats tool_changed",
			build: func() *world {
				return healthyWorld().approval(func(a *ApprovalState) {
					a.Disabled = true
					a.Status = ApprovalStatusChanged
				})
			},
			ref:    ToolRef{ID: id},
			want:   ReasonToolBlockedByUser,
			reason: "approving the change would still leave the tool disabled",
		},
		{
			name: "tool_changed is not collapsed into tool_pending_approval",
			build: func() *world {
				return healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusChanged })
			},
			ref:    ToolRef{ID: id},
			want:   ReasonToolChanged,
			reason: "a status field holds one value; the pair encodes changed != pending (research D2)",
		},
		{
			name: "pending beats hash_mismatch",
			build: func() *world {
				return healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending })
			},
			ref:    ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"},
			want:   ReasonToolPendingApproval,
			reason: "the pin cannot be re-locked before the tool is reviewed",
		},
		{
			name:   "hash_mismatch beats oauth_required",
			build:  func() *world { return healthyWorld().runtime(RuntimeStatePendingAuth) },
			ref:    ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"},
			want:   ReasonHashMismatch,
			reason: "logging in will not make a drifted definition match the pin",
		},
		{
			name:   "oauth_required beats server_unhealthy (PendingAuth is not a health failure)",
			build:  func() *world { return healthyWorld().runtime(RuntimeStatePendingAuth) },
			ref:    ToolRef{ID: id},
			want:   ReasonOAuthRequired,
			reason: "FR-007: deferred OAuth is non-retryable and needs a login, not a wait",
		},
		{
			name:   "server_unhealthy beats server_initializing",
			build:  func() *world { return healthyWorld().runtime(RuntimeStateError) },
			ref:    ToolRef{ID: id},
			want:   ReasonServerUnhealthy,
			reason: "one connection state per server; error is reported as error, not as startup",
		},
		{
			name: "server_initializing beats the annotation filters",
			build: func() *world {
				w := healthyWorld().annotations(nil).runtime(RuntimeStateConnecting)
				w.filters.readOnlyOnly = true
				return w
			},
			ref:    ToolRef{ID: id},
			want:   ReasonServerInitializing,
			reason: "annotations from a half-connected server are not yet a policy verdict",
		},
		{
			name: "annotation filters beat ready",
			build: func() *world {
				w := healthyWorld().annotations(&config.ToolAnnotations{ReadOnlyHint: boolPtr(false)})
				w.filters.readOnlyOnly = true
				return w
			},
			ref:    ToolRef{ID: id},
			want:   ReasonPolicyFiltered,
			reason: "the final gate before ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := evalOne(t, tt.build(), tt.ref)
			assert.Equal(t, tt.want, res.Reason, tt.reason)
		})
	}
}

// The first annotation filter that excludes owns the omission (spec 094 order:
// read_only_only -> exclude_destructive -> exclude_open_world).
func TestEvaluate_AnnotationFilterOrder(t *testing.T) {
	w := healthyWorld().annotations(nil)
	w.filters.readOnlyOnly = true
	w.filters.excludeDestructive = true
	w.filters.excludeOpenWorld = true

	res := evalOne(t, w, ToolRef{ID: id})
	assert.Equal(t, ReasonMissingAnnotation, res.Reason)
	assert.Contains(t, res.Detail, "read_only_only", "the first filter owns the omission")

	// With the read-only filter satisfied, the destructive filter owns it.
	w2 := healthyWorld().annotations(&config.ToolAnnotations{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true)})
	w2.filters.excludeDestructive = true
	w2.filters.excludeOpenWorld = true
	res2 := evalOne(t, w2, ToolRef{ID: id})
	// readOnlyHint=true is inherently non-destructive (frozen spec-094
	// shortcut), so exclude_open_world owns this one.
	assert.Equal(t, ReasonMissingAnnotation, res2.Reason)
	assert.Contains(t, res2.Detail, "exclude_open_world")
}

// --- hash pins (FR-011) -----------------------------------------------------

func TestPin_FormatAndParseRoundTrip(t *testing.T) {
	pin := FormatPin(2, "9f2c41ab")
	assert.Equal(t, "sha256/v2:9f2c41ab", pin)

	v, h, err := ParsePin(pin)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), v)
	assert.Equal(t, "9f2c41ab", h)

	for _, bad := range []string{"", "abc123", "sha256:abc", "sha256/v:abc", "sha256/vX:abc", "sha256/v2:"} {
		_, _, err := ParsePin(bad)
		assert.Error(t, err, "pin %q must not parse", bad)
	}
}

func TestEvaluate_PinMatchingIsReady(t *testing.T) {
	res := evalOne(t, healthyWorld(), ToolRef{ID: id, PinHash: "sha256/v2:abc123"})
	assert.Equal(t, StatusReady, res.Status)
}

func TestEvaluate_PinSchemaVersionBumpIsDistinguishable(t *testing.T) {
	// Same hex, different schema version: a proxy-side algorithm bump, not
	// upstream drift. Same reason code, different detail (research D4).
	res := evalOne(t, healthyWorld(), ToolRef{ID: id, PinHash: "sha256/v1:abc123"})
	assert.Equal(t, ReasonHashMismatch, res.Reason)
	assert.Contains(t, res.Detail, "Hash schema changed")

	drift := evalOne(t, healthyWorld(), ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"})
	assert.Equal(t, ReasonHashMismatch, drift.Reason)
	assert.NotContains(t, drift.Detail, "Hash schema changed")
}

func TestEvaluate_PinUnverifiableFailsClosed(t *testing.T) {
	// No stored hash at all: a pin that cannot be checked must not pass a gate
	// whose entire purpose is detecting drift.
	w := healthyWorld().approval(func(a *ApprovalState) { a.CurrentHash = "" })
	res := evalOne(t, w, ToolRef{ID: id, PinHash: "sha256/v2:abc123"})
	assert.Equal(t, ReasonHashMismatch, res.Reason)
	assert.Contains(t, res.Detail, "cannot be verified")

	malformed := evalOne(t, healthyWorld(), ToolRef{ID: id, PinHash: "not-a-pin"})
	assert.Equal(t, ReasonHashMismatch, malformed.Reason)
	assert.Contains(t, malformed.Detail, "Invalid pin format")
}

func TestEvaluate_PinsMapIsAFallbackForRefPin(t *testing.T) {
	w := healthyWorld()
	w.pins = map[string]string{id: "sha256/v2:deadbeef"}
	assert.Equal(t, ReasonHashMismatch, evalOne(t, w, ToolRef{ID: id}).Reason)

	// An explicit ref pin wins over the map.
	assert.Equal(t, StatusReady, evalOne(t, w, ToolRef{ID: id, PinHash: "sha256/v2:abc123"}).Status)
}

// --- ids, batching, infra errors -------------------------------------------

func TestEvaluate_MalformedIDIsPerIDNotRequestError(t *testing.T) {
	w := healthyWorld()
	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{
		{ID: "no-separator"},
		{ID: id},
		{ID: ":empty-server"},
		{ID: "empty-tool:"},
	})
	require.NoError(t, err, "one bad entry must not mask the rest")
	require.Len(t, results, 4)

	assert.Equal(t, ReasonNotFound, results[0].Reason)
	assert.Contains(t, results[0].Detail, "<server>:<tool>")
	assert.Equal(t, StatusReady, results[1].Status)
	assert.Equal(t, ReasonNotFound, results[2].Reason)
	assert.Equal(t, ReasonNotFound, results[3].Reason)
}

func TestEvaluate_ResultsFollowRequestOrderAndEchoIDs(t *testing.T) {
	w := healthyWorld()
	refs := []ToolRef{{ID: "gh:missing"}, {ID: id}, {ID: "ghost:tool"}}
	results, err := Evaluate(context.Background(), w.ctx(), refs)
	require.NoError(t, err)
	require.Len(t, results, 3)
	for i, ref := range refs {
		assert.Equal(t, ref.ID, results[i].ID)
	}
}

// FR-006 / doc invariant 2: an infrastructure read failure is an error, never a
// fabricated reason code.
func TestEvaluate_InfraErrorsAreErrorsNotReasons(t *testing.T) {
	t.Run("index read", func(t *testing.T) {
		w := healthyWorld()
		w.index.toolsErr = errBoom
		_, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}})
		require.Error(t, err)
	})
	t.Run("approval read", func(t *testing.T) {
		w := healthyWorld()
		w.approvals.err = errBoom
		_, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}})
		require.Error(t, err)
	})
	t.Run("server policy read", func(t *testing.T) {
		w := healthyWorld()
		w.policy.serverErr = errBoom
		_, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}})
		require.Error(t, err)
	})
	t.Run("tool config policy read", func(t *testing.T) {
		w := healthyWorld()
		w.policy.deniedErr = errBoom
		_, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}})
		require.Error(t, err)
	})
	t.Run("suggestion corpus read", func(t *testing.T) {
		w := healthyWorld().unindex().forget()
		w.index.serversErr = errBoom
		_, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}})
		require.Error(t, err, "a failed corpus build must not silently produce a suggestion-free not_found")
	})
}

func TestEvaluate_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Evaluate(ctx, healthyWorld().ctx(), []ToolRef{{ID: id}})
	require.ErrorIs(t, err, context.Canceled)
}

// A missing snapshot entry is not evidence of ill health: the evaluator makes no
// connection-state claim (the served surface refuses with 503 when the runtime
// is unavailable altogether).
func TestEvaluate_NoRuntimeEntryMakesNoConnectionClaim(t *testing.T) {
	w := healthyWorld()
	delete(w.state.states, srv)
	assert.Equal(t, StatusReady, evalOne(t, w, ToolRef{ID: id}).Status)

	w2 := healthyWorld()
	w2.state = nil
	res, err := Evaluate(context.Background(), w2.ctx(), []ToolRef{{ID: id}})
	require.NoError(t, err)
	assert.Equal(t, StatusReady, res[0].Status)
}

// The spec-044 diagnostic may sharpen the server_unhealthy action, but only
// within the existing health vocabulary.
func TestEvaluate_UnhealthyActionOverride(t *testing.T) {
	w := healthyWorld()
	w.state.states[srv] = ServerRuntime{State: RuntimeStateError, Detail: "process exited: status 127", Action: "restart"}
	res := evalOne(t, w, ToolRef{ID: id})
	assert.Equal(t, ReasonServerUnhealthy, res.Reason)
	assert.Equal(t, "restart", res.Action)
	assert.Equal(t, "process exited: status 127", res.Detail)

	w2 := healthyWorld()
	w2.state.states[srv] = ServerRuntime{State: RuntimeStateError, Action: "sacrifice_a_goat"}
	assert.Equal(t, "view_logs", evalOne(t, w2, ToolRef{ID: id}).Action,
		"an action outside the health vocabulary falls back to the taxonomy default")
}

// The evaluator's tool-level verdicts must be exactly the shared classifier's,
// so preflight and dispatch cannot disagree (FR-002).
func TestEvaluate_UsesSharedClassifier(t *testing.T) {
	// auto_approve_tool_changes: a changed tool is ready (dispatch behavior).
	w := healthyWorld().autoApprove().approval(func(a *ApprovalState) { a.Status = ApprovalStatusChanged })
	assert.Equal(t, StatusReady, evalOne(t, w, ToolRef{ID: id}).Status)

	// Global quarantine off: pending does not gate either.
	w2 := healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending })
	w2.policy.quarantine = false
	assert.Equal(t, StatusReady, evalOne(t, w2, ToolRef{ID: id}).Status)

	// But a user block still applies in both cases.
	w3 := healthyWorld().autoApprove().approval(func(a *ApprovalState) { a.Disabled = true })
	assert.Equal(t, ReasonToolBlockedByUser, evalOne(t, w3, ToolRef{ID: id}).Reason)
}
