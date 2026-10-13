package preflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 FR-006 / T077 (contracts/entitlement-predicate.md §3): the
// empty-list two-semantics trap. storage.intersectAllowedServers hands an
// unentitled owned token a non-nil EMPTY AllowedServers, which every MCP
// consumer reads as deny-all — but normalizeTokenServers read "len == 0" as
// "no restriction", so the same token was UNRESTRICTED on the REST preflight
// door (memory/project_agent_token_empty_allowlist_two_semantics.md).
//
// ScopeInputs.Restricted closes it: set for every non-administrator, it makes
// an empty TokenServers a deny-all scope. Unrestricted callers (operator,
// nil context) keep "empty = no restriction".
func TestResolveScope_RestrictedEmptyTokenServersIsDenyAll(t *testing.T) {
	scope := ResolveScope(ScopeInputs{Restricted: true, TokenServers: []string{}})
	require.NotNil(t, scope, "a restricted caller with an empty token scope must get a real (deny-all) scope, not nil (unrestricted)")
	assert.False(t, scope.Allows("fs"))
	assert.False(t, scope.Allows("github"))
	assert.Empty(t, scope.ServerNames())

	nilScope := ResolveScope(ScopeInputs{Restricted: true, TokenServers: nil})
	require.NotNil(t, nilScope, "nil and [] must mean the same thing for a restricted caller")
	assert.False(t, nilScope.Allows("fs"))
}

func TestResolveScope_RestrictedKeepsExplicitGrantsAndWildcard(t *testing.T) {
	// An explicit grant is unchanged by the marker.
	scope := ResolveScope(ScopeInputs{Restricted: true, TokenServers: []string{"fs"}})
	require.NotNil(t, scope)
	assert.True(t, scope.Allows("fs"))
	assert.False(t, scope.Allows("github"))

	// A literal "*" (an administrator-owned token, FR-009) stays unrestricted
	// even under the marker: the marker only decides what EMPTY means.
	star := ResolveScope(ScopeInputs{Restricted: true, TokenServers: []string{"*"}})
	assert.Nil(t, star, "a wildcard grant is unrestricted whatever the marker says")
}

func TestResolveScope_UnrestrictedEmptyStaysUnrestricted(t *testing.T) {
	// Operator callers never set the marker; their empty TokenServers is no
	// restriction, exactly as before.
	assert.Nil(t, ResolveScope(ScopeInputs{TokenServers: nil}))
	assert.Nil(t, ResolveScope(ScopeInputs{TokenServers: []string{}}))
}

func TestResolveScope_RestrictedEmptyIntersectsProfileToDenyAll(t *testing.T) {
	// Restricted + empty token scope ∩ a requested profile = deny-all: the
	// profile cannot widen a caller who is entitled to nothing.
	scope := ResolveScope(ScopeInputs{
		Restricted:              true,
		TokenServers:            []string{},
		RequestedProfileName:    "readonly",
		RequestedProfileServers: []string{"fs", "docs"},
	})
	require.NotNil(t, scope)
	assert.Equal(t, "readonly", scope.Name())
	assert.False(t, scope.Allows("fs"))
	assert.False(t, scope.Allows("docs"))
}
