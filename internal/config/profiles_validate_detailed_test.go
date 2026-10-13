package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 108-f F6: the validator's fatal errors are typed so REST can answer
// 400 {error, field} without parsing text, and the text itself is unchanged.
func TestValidateProfilesDetailed_TypedFields(t *testing.T) {
	self := []string{"a"}
	cases := []struct {
		name    string
		profile ProfileConfig
		field   string
		text    string
	}{
		{"bad slug", ProfileConfig{Name: "Bad Name"}, "name", "invalid profile name"},
		{"reserved", ProfileConfig{Name: "code"}, "name", "is reserved"},
		{"max_tier", ProfileConfig{Name: "a", MaxTier: "bogus"}, "max_tier", "invalid max_tier"},
		{"unannotated", ProfileConfig{Name: "a", Unannotated: "bogus"}, "unannotated", "invalid unannotated"},
		{"title", ProfileConfig{Name: "a", Title: strings.Repeat("x", 81)}, "title", "title too long"},
		{"description", ProfileConfig{Name: "a", Description: strings.Repeat("x", 501)}, "description", "description too long"},
		{"switchable_to", ProfileConfig{Name: "a", SwitchableTo: &self}, "switchable_to", "cannot include the profile itself"},
		{"allow", ProfileConfig{Name: "a", Tools: &ProfileToolRules{Allow: []string{"no-colon"}}}, "tools.allow", "invalid tool pattern"},
		{"deny", ProfileConfig{Name: "a", Tools: &ProfileToolRules{Deny: []string{"no-colon"}}}, "tools.deny", "invalid tool pattern"},
		{"classify pattern", ProfileConfig{Name: "a", Tools: &ProfileToolRules{Classify: map[string]string{"s:*": "read"}}}, "tools.classify", "invalid tool pattern"},
		{"classify tier", ProfileConfig{Name: "a", Tools: &ProfileToolRules{Classify: map[string]string{"s:t": "root"}}}, "tools.classify", "invalid classify tier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateProfilesDetailed(&Config{Profiles: []ProfileConfig{tc.profile}})
			require.Error(t, err)
			var pe *ProfileValidationError
			require.True(t, errors.As(err, &pe), "want *ProfileValidationError, got %T", err)
			assert.Equal(t, tc.field, pe.Field)
			assert.Equal(t, 0, pe.Index)
			assert.Contains(t, err.Error(), tc.text)

			// The flat wrapper reports the identical message.
			_, flat := ValidateProfiles(&Config{Profiles: []ProfileConfig{tc.profile}})
			require.Error(t, flat)
			assert.Equal(t, err.Error(), flat.Error())
		})
	}
}

func TestValidateProfilesDetailed_WarningsNameTheirProfile(t *testing.T) {
	cfg := &Config{
		Servers:          []*ServerConfig{{Name: "known"}},
		AnonymousProfile: "ghost",
		Profiles: []ProfileConfig{
			{Name: "empty"},
			{Name: "unknown-srv", Servers: []string{"known", "nope"}},
		},
	}
	warnings, err := ValidateProfilesDetailed(cfg)
	require.NoError(t, err)
	byProfile := map[string]string{}
	for _, w := range warnings {
		byProfile[w.Profile] += w.Message + "\n"
	}
	assert.Contains(t, byProfile["empty"], "no servers")
	assert.Contains(t, byProfile["unknown-srv"], `unknown server "nope"`)
	assert.Contains(t, byProfile[""], `anonymous_profile "ghost" does not exist`)

	flat, err := ValidateProfiles(cfg)
	require.NoError(t, err)
	assert.Len(t, flat, len(warnings))
}
