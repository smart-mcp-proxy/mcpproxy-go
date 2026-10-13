package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyEnforcementReady_After108D(t *testing.T) {
	require.True(t, PolicyEnforcementReady(), "108-d enforces Profiles v3 across execution and discovery")

	trueVal := true
	falseVal := false
	switchTo := []string{"other"}
	cases := []struct {
		name string
		set  func(*ProfileConfig)
	}{
		{"max_tier", func(p *ProfileConfig) { p.MaxTier = "read" }},
		{"unannotated", func(p *ProfileConfig) { p.Unannotated = "deny" }},
		{"tools", func(p *ProfileConfig) { p.Tools = &ProfileToolRules{Allow: []string{"a:list"}} }},
		{"code_execution", func(p *ProfileConfig) { p.CodeExecution = &falseVal }},
		{"management_tools", func(p *ProfileConfig) { p.ManagementTools = &trueVal }},
		{"switchable_to", func(p *ProfileConfig) { p.SwitchableTo = &switchTo }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ProfileConfig{Name: "prof", Servers: []string{"a"}}
			tc.set(&p)
			cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, Profiles: []ProfileConfig{p, {Name: "other", Servers: []string{"a"}}}}
			_, err := ValidateProfiles(cfg)
			require.NoError(t, err)
		})
	}

	t.Run("anonymous profile confinement is accepted", func(t *testing.T) {
		cfg := &Config{
			Servers:          []*ServerConfig{{Name: "a"}},
			AnonymousProfile: "legacy",
			Profiles:         []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}},
		}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Empty(t, warnings)
	})
}
