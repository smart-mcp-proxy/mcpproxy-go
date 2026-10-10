package preflight

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1548: preflight must evaluate the caller's effective profile tool
// policy, so a tool dispatch refuses under a profile never reads as ready.

func TestEvaluate_ProfileBlocked_OperatorTierNamesTheReason(t *testing.T) {
	w := healthyWorld().blockByProfile()
	res := evalOne(t, w, ToolRef{ID: id})

	assert.Equal(t, StatusUnavailable, res.Status)
	assert.Equal(t, ReasonToolBlockedByProfile, res.Reason)
	assert.False(t, res.Retryable, "a profile policy does not change by waiting")
	assert.Contains(t, res.Detail, "blocked by profile:", "the operator sees the dispatch refusal text")
	assert.Empty(t, res.Hash, "no pin on a failure")
	assert.Equal(t, VerdictBlocked, VerdictForResults([]Result{res}))
	assert.Equal(t, ExitBlocked, ExitCode(VerdictForResults([]Result{res})))
}

func TestEvaluate_ProfileBlocked_AgentTierIsScopeSilentNotFound(t *testing.T) {
	blocked := healthyWorld().blockByProfile()
	blocked.tier = TierAgentToken
	got := evalOne(t, blocked, ToolRef{ID: id})

	absent := healthyWorld().unindex().forget()
	absent.tier = TierAgentToken
	want := evalOne(t, absent, ToolRef{ID: id})

	require.Equal(t, ReasonNotFound, want.Reason)
	assert.Equal(t, want, got, "a profile-excluded tool must be byte-identical to an absent one at the agent-token tier")
	assert.NotContains(t, got.Detail, "profile")
}

func TestEvaluate_ProfilePermits_StaysReady(t *testing.T) {
	w := healthyWorld()
	w.toolPolicy = &fakeToolPolicy{blocked: map[string]string{"gh:other": "x"}}
	res := evalOne(t, w, ToolRef{ID: id})

	assert.Equal(t, StatusReady, res.Status)
	assert.Equal(t, []string{id}, w.toolPolicy.asked, "the policy was consulted for the requested tool")
}

func TestEvaluate_NoToolPolicy_IsNotDefaultDeny(t *testing.T) {
	res := evalOne(t, healthyWorld(), ToolRef{ID: id})
	assert.Equal(t, StatusReady, res.Status)
}

func TestEvaluate_ProfilePrecedence(t *testing.T) {
	t.Run("unknown tool stays not_found, policy never consulted", func(t *testing.T) {
		w := healthyWorld().unindex().forget().blockByProfile()
		res := evalOne(t, w, ToolRef{ID: id})
		assert.Equal(t, ReasonNotFound, res.Reason)
		assert.Empty(t, w.toolPolicy.asked)
	})
	t.Run("server_quarantined beats tool_blocked_by_profile", func(t *testing.T) {
		res := evalOne(t, healthyWorld().quarantine().blockByProfile(), ToolRef{ID: id})
		assert.Equal(t, ReasonServerQuarantined, res.Reason)
	})
	t.Run("server_not_in_scope beats tool_blocked_by_profile", func(t *testing.T) {
		res := evalOne(t, healthyWorld().outOfScope().blockByProfile(), ToolRef{ID: id})
		assert.Equal(t, ReasonServerNotInScope, res.Reason)
	})
	t.Run("tool_blocked_by_profile beats tool_denied_by_config", func(t *testing.T) {
		res := evalOne(t, healthyWorld().denyByConfig().blockByProfile(), ToolRef{ID: id})
		assert.Equal(t, ReasonToolBlockedByProfile, res.Reason)
	})
	t.Run("tool_blocked_by_profile beats tool_pending_approval", func(t *testing.T) {
		w := healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending }).blockByProfile()
		res := evalOne(t, w, ToolRef{ID: id})
		assert.Equal(t, ReasonToolBlockedByProfile, res.Reason)
	})
	t.Run("tool_blocked_by_profile beats a hash mismatch", func(t *testing.T) {
		res := evalOne(t, healthyWorld().blockByProfile(), ToolRef{ID: id, PinHash: "sha256/v2:nope"})
		assert.Equal(t, ReasonToolBlockedByProfile, res.Reason)
	})
	t.Run("tool_blocked_by_profile beats a retryable connection state", func(t *testing.T) {
		res := evalOne(t, healthyWorld().runtime(RuntimeStateConnecting).blockByProfile(), ToolRef{ID: id})
		assert.Equal(t, ReasonToolBlockedByProfile, res.Reason,
			"waiting cannot clear a policy refusal, so it must not read as retryable")
	})
}

// did_you_mean must never name a tool the caller's profile hides.
func TestEvaluate_ProfileBlockedToolsNeverSuggested(t *testing.T) {
	w := healthyWorld()
	w.tier = TierAgentToken
	w.index.tools[srv] = append(w.index.tools[srv], IndexedTool{
		Name:        srv + ":payroll",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	})
	w.toolPolicy = &fakeToolPolicy{blocked: map[string]string{srv + ":payroll": "denied"}}

	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: srv + ":payrol"}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, ReasonNotFound, results[0].Reason)
	assert.NotContains(t, results[0].DidYouMean, srv+":payroll", "a hidden tool must not be suggested")

	// Control: without the policy the same typo DOES suggest it, so the
	// assertion above is not vacuous.
	w.toolPolicy = nil
	results, err = Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: srv + ":payrol"}})
	require.NoError(t, err)
	assert.Contains(t, results[0].DidYouMean, srv+":payroll")
}

// A batch mixing permitted and blocked tools aggregates to blocked.
func TestEvaluate_ProfileMixedBatchIsBlocked(t *testing.T) {
	w := healthyWorld()
	w.index.tools[srv] = append(w.index.tools[srv], IndexedTool{Name: srv + ":write_it"})
	w.approvals.records[srv+":write_it"] = &ApprovalState{Status: ApprovalStatusApproved, CurrentHash: "h", HashSchemaVersion: 2}
	w.toolPolicy = &fakeToolPolicy{blocked: map[string]string{srv + ":write_it": "above cap"}}

	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}, {ID: srv + ":write_it"}})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, StatusReady, results[0].Status)
	assert.Equal(t, ReasonToolBlockedByProfile, results[1].Reason)
	assert.Equal(t, VerdictBlocked, VerdictForResults(results))
}

// At the agent-token tier a hidden tool must be indistinguishable from an
// absent one in EVERY server state, including the not-Ready states where an
// absent id answers a connection verdict instead of not_found.
func TestEvaluate_ProfileHidden_IndistinguishableFromAbsentInEveryState(t *testing.T) {
	states := []ServerRuntimeState{RuntimeStateReady, RuntimeStateConnecting, RuntimeStateDisconnected, RuntimeStatePendingAuth, RuntimeStateError}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			hiddenIndexed := healthyWorld().runtime(state).blockByProfile()
			hiddenIndexed.tier = TierAgentToken

			hiddenRecordOnly := healthyWorld().runtime(state).unindex().blockByProfile()
			hiddenRecordOnly.tier = TierAgentToken

			absent := healthyWorld().runtime(state).unindex().forget()
			absent.tier = TierAgentToken
			want := evalOne(t, absent, ToolRef{ID: id})

			assert.Equal(t, want, evalOne(t, hiddenIndexed, ToolRef{ID: id}), "indexed hidden tool")
			assert.Equal(t, want, evalOne(t, hiddenRecordOnly, ToolRef{ID: id}), "approval-record-only hidden tool")
		})
	}
}
