package config

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPolicyEnforcementReady_RollsOutGate pins the FR-009a rollout gate: with
// PolicyEnforcementReady() false (the value shipped from 108-a until
// 108-d), each of the six v3 policy fields AND a non-empty
// anonymous_profile (known or unknown, data-model.md §1) are a fatal
// validation error with the exact data-model.md §1 text; a legacy profile
// still loads. 108-d's T055a asserts the constant flips to true and inverts
// this test.
func TestPolicyEnforcementReady_RollsOutGate(t *testing.T) {
	require.False(t, PolicyEnforcementReady(), "must ship false until 108-d lands the last execution gate (FR-009a)")

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

	t.Run("anonymous_profile naming a known profile is fatal while the gate is closed", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, AnonymousProfile: "legacy", Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}}}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("anonymous_profile naming an unknown profile is fatal while the gate is closed", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "missing"}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("anonymous_profile is fatal even with zero profiles configured", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "legacy"}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("test-only override admits v3 fields", func(t *testing.T) {
		EnablePolicyForTest(t)
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, MaxTier: "read"}}}
		_, err := ValidateProfiles(cfg)
		require.NoError(t, err)
	})

	t.Run("test-only override admits anonymous_profile", func(t *testing.T) {
		EnablePolicyForTest(t)
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, AnonymousProfile: "legacy", Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Empty(t, warnings)
	})

	t.Run("override closes the gate again once the test that opened it ends", func(t *testing.T) {
		require.False(t, PolicyEnforcementReady())
		t.Run("nested", func(t *testing.T) {
			EnablePolicyForTest(t)
			require.True(t, PolicyEnforcementReady())
		})
		require.False(t, PolicyEnforcementReady(), "tb.Cleanup from the nested subtest must have closed the gate")
	})
}

// TestEnablePolicyForTest_PanicsOutsideATestBinary builds a plain `main`
// package that calls config.EnablePolicyForTest(nil) and runs it with `go
// build`+exec (not `go test`), asserting the override is unreachable outside
// a test binary: testing.Testing() is false there, so the panic fires (T004a
// item 2, zcode review round 2: "the override cannot ship").
func TestEnablePolicyForTest_PanicsOutsideATestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go build subprocess probe in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "overrideprobe")
	build := exec.Command("go", "build", "-o", bin, "./testdata/overrideprobe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build overrideprobe: %v\n%s", err, out)
	}

	out, err := exec.Command(bin).CombinedOutput()
	require.Error(t, err, "overrideprobe must exit non-zero: EnablePolicyForTest must panic outside a test binary")
	require.Contains(t, string(out), "EnablePolicyForTest called outside a test binary")
}
