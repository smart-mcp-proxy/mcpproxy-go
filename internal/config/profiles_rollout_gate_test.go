package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPolicyEnforcementReady_RollsOutGate pins the FR-009a rollout gate: with
// PolicyEnforcementReady false (the value shipped from 108-a until 108-d),
// each of the six v3 policy fields is a fatal validation error with the
// exact data-model.md §1 text; a legacy profile and anonymous_profile still
// load. 108-d's T055a asserts the constant flips to true and inverts this
// test.
func TestPolicyEnforcementReady_RollsOutGate(t *testing.T) {
	require.False(t, PolicyEnforcementReady, "must ship false until 108-d lands the last execution gate (FR-009a)")

	trueVal := true
	falseVal := false
	switchTo := []string{"other"}

	cases := []struct {
		field string
		set   func(p *ProfileConfig)
	}{
		{"max_tier", func(p *ProfileConfig) { p.MaxTier = "read" }},
		{"unannotated", func(p *ProfileConfig) { p.Unannotated = "deny" }},
		{"tools", func(p *ProfileConfig) { p.Tools = &ProfileToolRules{Allow: []string{"a:list"}} }},
		{"code_execution", func(p *ProfileConfig) { p.CodeExecution = &falseVal }},
		{"management_tools", func(p *ProfileConfig) { p.ManagementTools = &trueVal }},
		{"switchable_to", func(p *ProfileConfig) { p.SwitchableTo = &switchTo }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			p := ProfileConfig{Name: "prof", Servers: []string{"a"}}
			tc.set(&p)
			cfg := &Config{Profiles: []ProfileConfig{p, {Name: "other", Servers: []string{"a"}}}}

			_, err := ValidateProfiles(cfg)
			require.EqualError(t, err, "profiles[0]: "+tc.field+" is not supported by this build (Profiles v3 enforcement incomplete)")
		})
	}

	t.Run("legacy profile still loads", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}, Title: "Legacy"}}}
		_, err := ValidateProfiles(cfg)
		require.NoError(t, err)
	})

	t.Run("anonymous_profile still loads", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, AnonymousProfile: "legacy", Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Empty(t, warnings)
	})

	t.Run("test-only override admits v3 fields", func(t *testing.T) {
		defer SetPolicyEnforcementReadyForTest(true)()
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, MaxTier: "read"}}}
		_, err := ValidateProfiles(cfg)
		require.NoError(t, err)
	})

	t.Run("override restores the previous value on return", func(t *testing.T) {
		restore := SetPolicyEnforcementReadyForTest(true)
		require.True(t, PolicyEnforcementReady)
		restore()
		require.False(t, PolicyEnforcementReady)
	})
}
