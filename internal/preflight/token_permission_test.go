package preflight

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1548 follow-up: preflight must apply the credential's permission tier
// (agent-token permissions read / write / destructive) with the decision
// dispatch makes, so a tool the caller's token may not call is never ready.

func TestEvaluate_TokenPermission_BlockedAtBothTiers(t *testing.T) {
	for _, tier := range []Tier{TierOperator, TierAgentToken} {
		t.Run(string(tier), func(t *testing.T) {
			w := healthyWorld().lackTokenPermission()
			w.tier = tier
			res := evalOne(t, w, ToolRef{ID: id})

			assert.Equal(t, StatusUnavailable, res.Status)
			assert.Equal(t, ReasonTokenPermissionDenied, res.Reason)
			assert.False(t, res.Retryable, "a credential's permissions do not change by waiting")
			assert.Equal(t, fakeTokenPermissionDetail, res.Detail,
				"the tool is visible to this caller, so the dispatch refusal text is shown at every tier")
			assert.Empty(t, res.Hash)
			assert.Empty(t, res.DidYouMean)
			assert.Equal(t, VerdictBlocked, VerdictForResults([]Result{res}))
			assert.Equal(t, ExitBlocked, ExitCode(VerdictForResults([]Result{res})))
		})
	}
}

func TestEvaluate_TokenPermission_PermittedStaysReady(t *testing.T) {
	w := healthyWorld()
	w.tokenPerms = &fakeTokenPermissions{blocked: map[string]string{"gh:other": "x"}}
	res := evalOne(t, w, ToolRef{ID: id})

	assert.Equal(t, StatusReady, res.Status)
	assert.Equal(t, []string{id}, w.tokenPerms.asked, "the gate was consulted for the requested tool")
}

func TestEvaluate_TokenPermission_NoReaderIsNotDefaultDeny(t *testing.T) {
	res := evalOne(t, healthyWorld(), ToolRef{ID: id})
	assert.Equal(t, StatusReady, res.Status)
}

func TestEvaluate_TokenPermissionPrecedence(t *testing.T) {
	t.Run("unknown tool stays not_found, gate never consulted", func(t *testing.T) {
		w := healthyWorld().unindex().forget().lackTokenPermission()
		res := evalOne(t, w, ToolRef{ID: id})
		assert.Equal(t, ReasonNotFound, res.Reason)
		assert.Empty(t, w.tokenPerms.asked)
	})
	t.Run("server_quarantined beats token_permission_denied", func(t *testing.T) {
		res := evalOne(t, healthyWorld().quarantine().lackTokenPermission(), ToolRef{ID: id})
		assert.Equal(t, ReasonServerQuarantined, res.Reason)
	})
	t.Run("tool_blocked_by_profile beats token_permission_denied (operator tier)", func(t *testing.T) {
		res := evalOne(t, healthyWorld().blockByProfile().lackTokenPermission(), ToolRef{ID: id})
		assert.Equal(t, ReasonToolBlockedByProfile, res.Reason, "dispatch runs the profile gate before the token permission gate")
	})
	t.Run("a profile-hidden tool stays scope-silent at the agent-token tier", func(t *testing.T) {
		hidden := healthyWorld().blockByProfile().lackTokenPermission()
		hidden.tier = TierAgentToken
		absent := healthyWorld().unindex().forget()
		absent.tier = TierAgentToken
		assert.Equal(t, evalOne(t, absent, ToolRef{ID: id}), evalOne(t, hidden, ToolRef{ID: id}),
			"the permission verdict must not reveal a tool the profile hides")
	})
	t.Run("token_permission_denied beats tool_denied_by_config", func(t *testing.T) {
		res := evalOne(t, healthyWorld().denyByConfig().lackTokenPermission(), ToolRef{ID: id})
		assert.Equal(t, ReasonTokenPermissionDenied, res.Reason)
	})
	t.Run("token_permission_denied beats tool_pending_approval", func(t *testing.T) {
		w := healthyWorld().approval(func(a *ApprovalState) { a.Status = ApprovalStatusPending }).lackTokenPermission()
		assert.Equal(t, ReasonTokenPermissionDenied, evalOne(t, w, ToolRef{ID: id}).Reason)
	})
	t.Run("token_permission_denied beats a retryable connection state", func(t *testing.T) {
		res := evalOne(t, healthyWorld().runtime(RuntimeStateConnecting).lackTokenPermission(), ToolRef{ID: id})
		assert.Equal(t, ReasonTokenPermissionDenied, res.Reason, "waiting cannot clear a permission refusal")
	})
}

// When the tool's tier cannot be established (no authoritative annotations:
// dispatch then fails closed to destructive), the evaluator does not turn a
// transient connection state into a permanent refusal, but it never reports
// ready either.
func TestEvaluate_TokenPermission_IndeterminateTier(t *testing.T) {
	t.Run("a not-ready server answers its connection verdict", func(t *testing.T) {
		res := evalOne(t, healthyWorld().runtime(RuntimeStateDisconnected).tokenTierUnknown(), ToolRef{ID: id})
		assert.Equal(t, ReasonServerUnhealthy, res.Reason)
	})
	t.Run("an otherwise-ready tool is refused, never ready", func(t *testing.T) {
		res := evalOne(t, healthyWorld().tokenTierUnknown(), ToolRef{ID: id})
		assert.Equal(t, StatusUnavailable, res.Status)
		assert.Equal(t, ReasonTokenPermissionDenied, res.Reason)
	})
	t.Run("an earlier tool-level gate still owns the verdict", func(t *testing.T) {
		res := evalOne(t, healthyWorld().denyByConfig().tokenTierUnknown(), ToolRef{ID: id})
		assert.Equal(t, ReasonToolDeniedByConfig, res.Reason)
	})
}

// A batch mixing a permitted read and a write the token may not call
// aggregates to blocked, and only the write is refused.
func TestEvaluate_TokenPermissionMixedBatch(t *testing.T) {
	w := healthyWorld()
	w.index.tools[srv] = append(w.index.tools[srv], IndexedTool{Name: srv + ":write_it"})
	w.approvals.records[srv+":write_it"] = &ApprovalState{Status: ApprovalStatusApproved, CurrentHash: "h", HashSchemaVersion: 2}
	w.tokenPerms = &fakeTokenPermissions{blocked: map[string]string{srv + ":write_it": "Permission denied"}}
	w.tier = TierAgentToken

	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: id}, {ID: srv + ":write_it"}})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, StatusReady, results[0].Status)
	assert.Equal(t, ReasonTokenPermissionDenied, results[1].Reason)
	assert.Equal(t, VerdictBlocked, VerdictForResults(results))
}

// A permission-refused tool stays visible to discovery (retrieve_tools lists
// it), so a typo may still suggest it: suggestions are not narrowed by the
// permission tier.
func TestEvaluate_TokenPermissionDoesNotNarrowSuggestions(t *testing.T) {
	w := healthyWorld().lackTokenPermission()
	w.tier = TierAgentToken
	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{{ID: srv + ":syn"}})
	require.NoError(t, err)
	assert.Contains(t, results[0].DidYouMean, id)
}
