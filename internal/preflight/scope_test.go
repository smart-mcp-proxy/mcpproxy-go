package preflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScope_NilIsUnrestricted(t *testing.T) {
	var s *Scope
	assert.True(t, s.Allows("anything"))
	assert.Empty(t, s.Name())
	assert.Nil(t, s.ServerNames())
}

func TestScope_Membership(t *testing.T) {
	s := NewScope("readonly", []string{"fs", "web"})
	assert.True(t, s.Allows("fs"))
	assert.True(t, s.Allows("web"))
	assert.False(t, s.Allows("github"))
	assert.False(t, s.Allows(""), "the empty server name is never allowed by a real scope")
	assert.Equal(t, "readonly", s.Name())
	assert.Equal(t, []string{"fs", "web"}, s.ServerNames())

	deny := NewScope("empty", nil)
	assert.False(t, deny.Allows("fs"), "an empty scope is a legal deny-everything scope")
}

// ResolveScope: evaluation scope = token scope ∩ token pin ∩ requested profile.
func TestResolveScope_Intersections(t *testing.T) {
	tests := []struct {
		name         string
		in           ScopeInputs
		unrestricted bool
		wantName     string
		allowed      []string
		denied       []string
	}{
		{
			name:         "operator, no profile: unrestricted",
			in:           ScopeInputs{},
			unrestricted: true,
		},
		{
			name:         "agent token with wildcard scope and no pin: unrestricted",
			in:           ScopeInputs{TokenServers: []string{"*"}},
			unrestricted: true,
		},
		{
			name:     "token scope only",
			in:       ScopeInputs{TokenServers: []string{"fs", "web"}},
			allowed:  []string{"fs", "web"},
			denied:   []string{"github"},
			wantName: "",
		},
		{
			name: "requested profile only",
			in: ScopeInputs{
				RequestedProfileName:    "readonly",
				RequestedProfileServers: []string{"fs", "docs"},
			},
			allowed:  []string{"fs", "docs"},
			denied:   []string{"github"},
			wantName: "readonly",
		},
		{
			name: "token pin only (previously dropped on the REST path)",
			in: ScopeInputs{
				TokenPinName:    "work",
				TokenPinServers: []string{"jira", "gh"},
			},
			allowed:  []string{"jira", "gh"},
			denied:   []string{"fs"},
			wantName: "work",
		},
		{
			name: "token scope ∩ pin",
			in: ScopeInputs{
				TokenServers:    []string{"gh", "fs"},
				TokenPinName:    "work",
				TokenPinServers: []string{"gh", "jira"},
			},
			allowed:  []string{"gh"},
			denied:   []string{"fs", "jira"},
			wantName: "work",
		},
		{
			name: "token scope ∩ pin ∩ requested profile",
			in: ScopeInputs{
				TokenServers:            []string{"gh", "fs", "jira"},
				TokenPinName:            "work",
				TokenPinServers:         []string{"gh", "jira"},
				RequestedProfileName:    "review",
				RequestedProfileServers: []string{"gh", "fs"},
			},
			allowed:  []string{"gh"},
			denied:   []string{"fs", "jira"},
			wantName: "review",
		},
		{
			name: "a pinned token naming a disjoint profile can see nothing",
			in: ScopeInputs{
				TokenPinName:            "work",
				TokenPinServers:         []string{"gh"},
				RequestedProfileName:    "personal",
				RequestedProfileServers: []string{"fs"},
			},
			denied:   []string{"gh", "fs"},
			wantName: "personal",
		},
		{
			name: "an empty pinned profile denies everything",
			in: ScopeInputs{
				TokenServers:    []string{"gh"},
				TokenPinName:    "locked",
				TokenPinServers: nil,
			},
			denied:   []string{"gh"},
			wantName: "locked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveScope(tt.in)
			if tt.unrestricted {
				assert.Nil(t, got, "an unrestricted scope is the nil scope")
				return
			}
			if assert.NotNil(t, got) {
				assert.Equal(t, tt.wantName, got.Name())
			}
			for _, s := range tt.allowed {
				assert.True(t, got.Allows(s), "%s must be in scope", s)
			}
			for _, s := range tt.denied {
				assert.False(t, got.Allows(s), "%s must be out of scope", s)
			}
		})
	}
}

// The pin can never be widened by naming another profile: the result is the
// overlap, never the union.
func TestResolveScope_PinCannotBeWidened(t *testing.T) {
	got := ResolveScope(ScopeInputs{
		TokenPinName:            "work",
		TokenPinServers:         []string{"gh"},
		RequestedProfileName:    "everything",
		RequestedProfileServers: []string{"gh", "fs", "secrets"},
	})
	assert.Equal(t, []string{"gh"}, got.ServerNames())
}

func TestNormalizeTokenServers(t *testing.T) {
	_, restricted := normalizeTokenServers(nil)
	assert.False(t, restricted)

	_, restricted = normalizeTokenServers([]string{})
	assert.False(t, restricted)

	_, restricted = normalizeTokenServers([]string{"gh", "*"})
	assert.False(t, restricted, "a wildcard entry means the token does not restrict servers")

	servers, restricted := normalizeTokenServers([]string{"gh"})
	assert.True(t, restricted)
	assert.Equal(t, []string{"gh"}, servers)
}
