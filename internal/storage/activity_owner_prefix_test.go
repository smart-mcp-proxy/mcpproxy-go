package storage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A policy_decision row has no Arguments, so ownership comes from the
// persisted TokenPrefix; a legacy row still proves ownership through the
// _auth_token_prefix argument.
func TestActivityIdentityOwner_OwnsByPersistedPrefix(t *testing.T) {
	owner := &ActivityIdentityOwner{TokenName: "mine", TokenPrefix: "mcp_agt_aaaa"}

	policy := scopeRec("policy", func(r *ActivityRecord) {
		r.Type = ActivityTypePolicyDecision
		r.TokenName = "mine"
		r.TokenPrefix = "mcp_agt_aaaa"
	})
	assert.True(t, owner.Owns(policy), "a record with no Arguments is owned by its persisted prefix")

	other := scopeRec("other", func(r *ActivityRecord) {
		r.TokenName = "mine"
		r.TokenPrefix = "mcp_agt_bbbb"
	})
	assert.False(t, owner.Owns(other), "a mismatched prefix is not owned")

	otherName := scopeRec("name", func(r *ActivityRecord) {
		r.TokenName = "theirs"
		r.TokenPrefix = "mcp_agt_aaaa"
	})
	assert.False(t, owner.Owns(otherName), "prefix and name must both match")

	legacy := scopeRec("legacy", func(r *ActivityRecord) {
		r.TokenName = "mine"
		r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_agt_aaaa"}
	})
	assert.True(t, owner.Owns(legacy), "a pre-field record keeps proving ownership through its argument")

	unproven := scopeRec("unproven", func(r *ActivityRecord) { r.TokenName = "mine" })
	assert.False(t, owner.Owns(unproven), "no proof reads as foreign (fail closed)")
}

func TestActivityRecord_TokenPrefixRoundTripsInStorage(t *testing.T) {
	rec := scopeRec("rt", func(r *ActivityRecord) { r.TokenPrefix = "mcp_agt_aaaa" })
	data, err := rec.MarshalBinary()
	require.NoError(t, err)
	var back ActivityRecord
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, "mcp_agt_aaaa", back.TokenPrefix)
}
