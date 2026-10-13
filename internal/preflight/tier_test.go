package preflight

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// twoServerWorld: "vis" is visible to the caller, "hidden" is configured and
// healthy but outside the scope. Both have exactly one indexed tool.
func twoServerWorld() *world {
	w := &world{
		index: &fakeIndex{
			serverOrder: []string{"vis", "hidden"},
			tools: map[string][]IndexedTool{
				"vis":    {{Name: "vis:alpha", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}}},
				"hidden": {{Name: "hidden:beta", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}}},
			},
		},
		approvals: &fakeApprovals{records: map[string]*ApprovalState{
			"vis:alpha":   {Status: ApprovalStatusApproved, CurrentHash: "aaa", HashSchemaVersion: 2},
			"hidden:beta": {Status: ApprovalStatusApproved, CurrentHash: "bbb", HashSchemaVersion: 2},
		}},
		state: &fakeState{states: map[string]ServerRuntime{
			"vis":    {State: RuntimeStateReady},
			"hidden": {State: RuntimeStateReady},
		}},
		policy: &fakePolicy{
			servers: map[string]ServerPolicy{
				"vis":    {Found: true, Enabled: true},
				"hidden": {Found: true, Enabled: true},
			},
			quarantine: true,
			denied:     map[string]bool{},
		},
		scope: NewScope("readonly", []string{"vis"}),
	}
	return w
}

// FR-013 scope-silence: at the agent-token tier an out-of-scope id's ENTIRE
// result must be byte-indistinguishable from an ordinary not_found. Comparing
// serialized bytes (not field-by-field asserts) is the point — a future field
// added to only one of the two paths fails this test.
func TestTier_AgentTokenScopeSilenceIsByteIndistinguishable(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierAgentToken

	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{
		{ID: "hidden:beta"}, // exists, out of scope
		{ID: "vis:beta"},    // in scope, does not exist
	})
	require.NoError(t, err)
	require.Len(t, results, 2)

	outOfScope, ordinary := results[0], results[1]
	assert.Equal(t, ReasonNotFound, outOfScope.Reason, "an out-of-scope id must never reveal server_not_in_scope to a token")

	// Normalize only the echoed id — everything else must match byte for byte.
	outOfScope.ID = ""
	ordinary.ID = ""
	a, err := json.Marshal(outOfScope)
	require.NoError(t, err)
	b, err := json.Marshal(ordinary)
	require.NoError(t, err)
	assert.Equal(t, string(b), string(a), "scope-masked not_found must be byte-identical to an ordinary not_found")
}

// The same silence applies one level up: an UNCONFIGURED server must be
// indistinguishable from an out-of-scope one at the agent-token tier, or a
// token can probe arbitrary names and learn which servers exist at all
// (server_not_configured vs not_found would be the oracle).
func TestTier_AgentTokenUnconfiguredServerIsScopeSilent(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierAgentToken

	results, err := Evaluate(context.Background(), w.ctx(), []ToolRef{
		{ID: "ghost:beta"},  // server not configured at all
		{ID: "hidden:beta"}, // configured, out of scope
	})
	require.NoError(t, err)
	require.Len(t, results, 2)

	unconfigured, hidden := results[0], results[1]
	assert.Equal(t, ReasonNotFound, unconfigured.Reason,
		"an unconfigured server must never reveal server_not_configured to a token")
	unconfigured.ID = ""
	hidden.ID = ""
	a, err := json.Marshal(unconfigured)
	require.NoError(t, err)
	b, err := json.Marshal(hidden)
	require.NoError(t, err)
	assert.Equal(t, string(b), string(a),
		"unconfigured and out-of-scope must be byte-identical at the token tier")
}

// A suggestion must never cross the scope boundary.
func TestTier_AgentTokenSuggestionsStayInScope(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierAgentToken
	// A near-miss for the hidden tool: "hidden:beto" is one edit from
	// "hidden:beta", which the caller must never be told about.
	w.policy.servers["hidden"] = ServerPolicy{Found: true, Enabled: true}

	res := evalOne(t, w, ToolRef{ID: "hidden:beto"})
	assert.Equal(t, ReasonNotFound, res.Reason)
	assert.Empty(t, res.DidYouMean, "no did_you_mean may name a tool on an out-of-scope server")
}

// Quarantined servers' names are never suggested (FR-013), even in scope.
func TestSuggestions_ExcludeQuarantinedServers(t *testing.T) {
	w := twoServerWorld()
	w.scope = nil // unrestricted operator view
	w.policy.servers["hidden"] = ServerPolicy{Found: true, Enabled: true, Quarantined: true}

	res := evalOne(t, w, ToolRef{ID: "hidden:betoo"})
	assert.Equal(t, ReasonServerQuarantined, res.Reason, "the quarantined server itself reports quarantine")

	// And a miss elsewhere must not surface the quarantined server's tools.
	miss := evalOne(t, w, ToolRef{ID: "vis:beta"})
	assert.Equal(t, ReasonNotFound, miss.Reason)
	for _, s := range miss.DidYouMean {
		assert.NotContains(t, s, "hidden:", "quarantined server names must never be suggested")
	}
}

// Operator tier gets the full diagnosis, including the profile-session note.
func TestTier_OperatorGetsServerNotInScopeWithProfileDetail(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierOperator

	res := evalOne(t, w, ToolRef{ID: "hidden:beta"})
	assert.Equal(t, ReasonServerNotInScope, res.Reason)
	assert.Equal(t, "configure", res.Action)
	assert.False(t, res.Retryable)
	assert.Contains(t, res.Detail, "readonly", "the detail names the profile")
	assert.Contains(t, res.Detail, "not_found", "the detail explains what a pinned session would see")
}

// An unnamed scope (e.g. a bare agent-token server list evaluated at the
// operator tier) still explains itself without inventing a profile name.
func TestTier_OperatorScopeWithoutProfileName(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierOperator
	w.scope = NewScope("", []string{"vis"})

	res := evalOne(t, w, ToolRef{ID: "hidden:beta"})
	assert.Equal(t, ReasonServerNotInScope, res.Reason)
	assert.Contains(t, res.Detail, "outside the evaluated scope")
}

// Hashes are operator-tier disclosure only.
func TestTier_HashDisclosure(t *testing.T) {
	operator := evalOne(t, healthyWorld(), ToolRef{ID: id})
	assert.Equal(t, "sha256/v2:abc123", operator.Hash)

	w := healthyWorld()
	w.tier = TierAgentToken
	agent := evalOne(t, w, ToolRef{ID: id})
	assert.Equal(t, StatusReady, agent.Status)
	assert.Empty(t, agent.Hash, "an agent token never receives hashes")
}

// A pin mismatch must not leak the current hash to a token either.
func TestTier_HashMismatchDetailWithholdsHashesFromTokens(t *testing.T) {
	w := healthyWorld()
	w.tier = TierAgentToken
	res := evalOne(t, w, ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"})
	assert.Equal(t, ReasonHashMismatch, res.Reason)
	assert.NotContains(t, res.Detail, "abc123", "the current hash must not appear in an agent-token detail")

	op := evalOne(t, healthyWorld(), ToolRef{ID: id, PinHash: "sha256/v2:deadbeef"})
	assert.Contains(t, op.Detail, "abc123", "the operator tier does get the full diagnosis")
}

// Profile semantics are "shared index + scope filter": an in-scope id resolves
// exactly as it does unscoped. (The evaluator has no way to reach ForProfile —
// IndexReader has no profile accessor at all.)
func TestProfileScope_UsesSharedIndexWithFilter(t *testing.T) {
	w := twoServerWorld()
	w.tier = TierOperator
	assert.Equal(t, StatusReady, evalOne(t, w, ToolRef{ID: "vis:alpha"}).Status)

	w.scope = nil
	assert.Equal(t, StatusReady, evalOne(t, w, ToolRef{ID: "hidden:beta"}).Status,
		"without a scope the operator view sees every configured server")
}
