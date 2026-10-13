package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scopeRec(id string, mut func(*ActivityRecord)) *ActivityRecord {
	r := &ActivityRecord{
		ID:         id,
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "list_issues",
		Status:     ActivityStatusSuccess,
		Timestamp:  time.Now(),
	}
	if mut != nil {
		mut(r)
	}
	return r
}

func TestActivityFilter_ProfileMatchesFieldThenLegacyMetadata(t *testing.T) {
	f := ActivityFilter{Profile: "work-readonly"}

	field := scopeRec("a", func(r *ActivityRecord) { r.Profile = "work-readonly" })
	legacy := scopeRec("b", func(r *ActivityRecord) { r.Metadata = map[string]interface{}{"profile": "work-readonly"} })
	both := scopeRec("c", func(r *ActivityRecord) {
		r.Profile = "work-full"
		r.Metadata = map[string]interface{}{"profile": "work-readonly"}
	})
	none := scopeRec("d", nil)

	assert.True(t, f.Matches(field))
	assert.True(t, f.Matches(legacy), "legacy metadata.profile must still match (FR-031, US3-4)")
	assert.False(t, f.Matches(both), "the first-class field wins over legacy metadata")
	assert.False(t, f.Matches(none))
}

func TestActivityFilter_ClientIDExact(t *testing.T) {
	f := ActivityFilter{ClientID: "cursor"}
	assert.True(t, f.Matches(scopeRec("a", func(r *ActivityRecord) { r.ClientID = "cursor" })))
	assert.False(t, f.Matches(scopeRec("b", func(r *ActivityRecord) { r.ClientID = "cursor-2" })))
	assert.False(t, f.Matches(scopeRec("c", func(r *ActivityRecord) { r.Metadata = map[string]interface{}{"client_id": "cursor"} })),
		"client reads the field only: pre-108 records have no client id")
}

func TestActivityFilter_ClientNameFieldThenLegacyMetadata(t *testing.T) {
	f := ActivityFilter{ClientName: "Cursor"}
	assert.True(t, f.Matches(scopeRec("a", func(r *ActivityRecord) { r.ClientName = "Cursor" })))
	assert.True(t, f.Matches(scopeRec("b", func(r *ActivityRecord) { r.Metadata = map[string]interface{}{"client_name": "Cursor"} })))
	assert.False(t, f.Matches(scopeRec("c", func(r *ActivityRecord) {
		r.ClientName = "Zed"
		r.Metadata = map[string]interface{}{"client_name": "Cursor"}
	})))
}

func TestActivityFilter_TokenMatchesFieldThenLegacyAuthArg(t *testing.T) {
	f := ActivityFilter{TokenName: "ci-bot"}
	assert.True(t, f.Matches(scopeRec("a", func(r *ActivityRecord) { r.TokenName = "ci-bot" })))
	assert.True(t, f.Matches(scopeRec("b", func(r *ActivityRecord) {
		r.Arguments = map[string]interface{}{"_auth_agent_name": "ci-bot"}
	})), "legacy Spec 028 _auth_agent_name argument must still match")
	assert.False(t, f.Matches(scopeRec("c", func(r *ActivityRecord) {
		r.TokenName = "other"
		r.Arguments = map[string]interface{}{"_auth_agent_name": "ci-bot"}
	})))
}

func TestActivityFilter_UnattributedSentinel(t *testing.T) {
	cases := []struct {
		name   string
		filter ActivityFilter
		set    func(*ActivityRecord)
	}{
		{"profile", ActivityFilter{Profile: ScopeFilterUnattributed}, func(r *ActivityRecord) { r.Profile = "p" }},
		{"client", ActivityFilter{ClientID: ScopeFilterUnattributed}, func(r *ActivityRecord) { r.ClientID = "c" }},
		{"client_name", ActivityFilter{ClientName: ScopeFilterUnattributed}, func(r *ActivityRecord) { r.ClientName = "c" }},
		{"token", ActivityFilter{TokenName: ScopeFilterUnattributed}, func(r *ActivityRecord) { r.TokenName = "t" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.filter
			assert.True(t, f.Matches(scopeRec("empty", nil)), "- matches the empty effective value")
			assert.False(t, f.Matches(scopeRec("set", tc.set)), "- never matches a set value")
		})
	}
	// Legacy fallbacks count as attributed.
	f := ActivityFilter{Profile: ScopeFilterUnattributed}
	assert.False(t, f.Matches(scopeRec("legacy", func(r *ActivityRecord) { r.Metadata = map[string]interface{}{"profile": "p"} })))
}

func TestActivityFilter_AuthorizationBeforeScope(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	inScope := scopeRec("in", func(r *ActivityRecord) { r.Profile = "work"; r.ServerName = "github" })
	outScope := scopeRec("out", func(r *ActivityRecord) { r.Profile = "work"; r.ServerName = "notion" })
	other := scopeRec("other", func(r *ActivityRecord) { r.Profile = "personal"; r.ServerName = "github" })
	for _, r := range []*ActivityRecord{inScope, outScope, other} {
		require.NoError(t, m.SaveActivity(r))
	}

	f := DefaultActivityFilter()
	f.AllowedServers = []string{"github"}
	f.Profile = "work"
	recs, total, err := m.ListActivities(f)
	require.NoError(t, err)
	assert.Equal(t, 1, total, "an out-of-scope record with the matching profile is never counted")
	require.Len(t, recs, 1)
	assert.Equal(t, "in", recs[0].ID)
}

func TestActivityFilter_IdentityOwnerSeesForeignAttributionAsEmpty(t *testing.T) {
	own := scopeRec("own", func(r *ActivityRecord) {
		r.TokenName = "mine"
		r.Profile = "work"
		r.ClientID = "cursor"
		r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_agt_aaaa"}
	})
	foreign := scopeRec("foreign", func(r *ActivityRecord) {
		r.TokenName = "theirs"
		r.Profile = "work"
		r.ClientID = "zed"
		r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_agt_bbbb"}
	})
	owner := &ActivityIdentityOwner{TokenName: "mine", TokenPrefix: "mcp_agt_aaaa"}

	f := ActivityFilter{TokenName: "theirs", IdentityOwner: owner}
	assert.False(t, f.Matches(foreign), "a scoped caller cannot probe a foreign token name")
	assert.False(t, f.Matches(own))

	f = ActivityFilter{TokenName: ScopeFilterUnattributed, IdentityOwner: owner}
	assert.True(t, f.Matches(foreign), "foreign attribution reads as empty")
	assert.False(t, f.Matches(own))

	f = ActivityFilter{ClientID: "zed", IdentityOwner: owner}
	assert.False(t, f.Matches(foreign))
	f = ActivityFilter{ClientID: "cursor", IdentityOwner: owner}
	assert.True(t, f.Matches(own))
	f = ActivityFilter{Profile: "work", IdentityOwner: owner}
	assert.True(t, f.Matches(own))
	assert.False(t, f.Matches(foreign))
	f = ActivityFilter{TokenName: "mine", IdentityOwner: owner}
	assert.True(t, f.Matches(own))
}

func TestActivityRecord_JSONOmitsEmptyAttribution(t *testing.T) {
	legacy := scopeRec("x", nil)
	legacy.Timestamp = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	data, err := json.Marshal(legacy)
	require.NoError(t, err)
	for _, key := range []string{"profile", "profile_source", "client_id", "client_name", "token_name", "block_reason"} {
		assert.NotContains(t, string(data), "\""+key+"\"", key)
	}
}
