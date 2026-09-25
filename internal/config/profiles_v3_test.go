package config

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProfileConfig_LegacyRoundTrip pins byte-identical round-tripping of a
// pre-108 profile (Spec 057/105): a {name, servers, title, description}
// profile is legacy (title/description are display-only, spec Definitions)
// and its JSON is unchanged by adding the Spec 108 struct fields (all
// omitempty, FR-002).
func TestProfileConfig_LegacyRoundTrip(t *testing.T) {
	src := `{"name":"work","servers":["github","linear"],"title":"Work","description":"my work servers"}`

	var p ProfileConfig
	require.NoError(t, json.Unmarshal([]byte(src), &p))
	require.True(t, p.IsLegacy(), "title/description must never make a profile non-legacy")

	out, err := json.Marshal(p)
	require.NoError(t, err)
	require.JSONEq(t, src, string(out))
}

// TestProfileConfig_V3FieldsParse pins that every v3 field round-trips and
// that setting any ONE of the six policy fields flips IsLegacy() false
// (spec Definitions, data-model.md §1).
func TestProfileConfig_V3FieldsParse(t *testing.T) {
	trueVal := true
	falseVal := false
	switchable := []string{"work-full"}

	cases := []struct {
		name string
		set  func(p *ProfileConfig)
	}{
		{"max_tier", func(p *ProfileConfig) { p.MaxTier = "read" }},
		{"unannotated", func(p *ProfileConfig) { p.Unannotated = "deny" }},
		{"tools", func(p *ProfileConfig) { p.Tools = &ProfileToolRules{Allow: []string{"github:list_issues"}} }},
		{"code_execution", func(p *ProfileConfig) { p.CodeExecution = &falseVal }},
		{"management_tools", func(p *ProfileConfig) { p.ManagementTools = &trueVal }},
		{"switchable_to", func(p *ProfileConfig) { p.SwitchableTo = &switchable }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ProfileConfig{Name: "work", Servers: []string{"github"}}
			require.True(t, p.IsLegacy())
			tc.set(&p)
			require.False(t, p.IsLegacy(), "%s must flip IsLegacy() false", tc.name)

			b, err := json.Marshal(p)
			require.NoError(t, err)
			var round ProfileConfig
			require.NoError(t, json.Unmarshal(b, &round))
			require.Equal(t, p, round, "%s must round-trip byte-for-byte", tc.name)
		})
	}
}

// TestProfileConfig_SwitchableToRoundTrip pins the *[]string pointer
// distinction (data-model.md §1): a profile saved with switchable_to: []
// through the real config save path reloads with a non-nil empty list and
// IsLegacy() false; one saved without the field reloads nil (legacy).
func TestProfileConfig_SwitchableToRoundTrip(t *testing.T) {
	empty := []string{}
	cfg := &Config{Profiles: []ProfileConfig{
		{Name: "explicit-none", Servers: []string{"github"}, SwitchableTo: &empty},
		{Name: "unset", Servers: []string{"github"}},
	}}

	b, err := json.Marshal(cfg)
	require.NoError(t, err)

	var round Config
	require.NoError(t, json.Unmarshal(b, &round))
	require.Len(t, round.Profiles, 2)

	explicit := round.Profiles[0]
	require.NotNil(t, explicit.SwitchableTo, "switchable_to: [] must survive the real config save path")
	require.Empty(t, *explicit.SwitchableTo)
	require.False(t, explicit.IsLegacy())

	unset := round.Profiles[1]
	require.Nil(t, unset.SwitchableTo, "an absent switchable_to must reload nil (legacy)")
	require.True(t, unset.IsLegacy())
}

// TestProfileConfig_IsLegacy_EveryPolicyField exhaustively pins that
// IsLegacy() is false as soon as ANY ONE of the six policy fields is set,
// and true for the zero value and for title/description alone.
func TestProfileConfig_IsLegacy_EveryPolicyField(t *testing.T) {
	require.True(t, (ProfileConfig{}).IsLegacy())
	require.True(t, (ProfileConfig{Name: "x", Servers: []string{"a"}, Title: "T", Description: "D"}).IsLegacy())

	trueVal := true
	empty := []string{}
	require.False(t, (ProfileConfig{MaxTier: "read"}).IsLegacy())
	require.False(t, (ProfileConfig{Unannotated: "deny"}).IsLegacy())
	require.False(t, (ProfileConfig{Tools: &ProfileToolRules{}}).IsLegacy())
	require.False(t, (ProfileConfig{CodeExecution: &trueVal}).IsLegacy())
	require.False(t, (ProfileConfig{ManagementTools: &trueVal}).IsLegacy())
	require.False(t, (ProfileConfig{SwitchableTo: &empty}).IsLegacy())
}

// TestProfileConfig_EffectiveUnannotated pins FR-003's default matrix.
func TestProfileConfig_EffectiveUnannotated(t *testing.T) {
	require.Equal(t, "deny", (ProfileConfig{MaxTier: "read"}).EffectiveUnannotated())
	require.Equal(t, "deny", (ProfileConfig{MaxTier: "write"}).EffectiveUnannotated())
	require.Equal(t, "as_read", (ProfileConfig{MaxTier: "destructive"}).EffectiveUnannotated())
	require.Equal(t, "as_read", (ProfileConfig{}).EffectiveUnannotated(), "legacy (no max_tier) defaults as_read")
	require.Equal(t, "as_write", (ProfileConfig{MaxTier: "read", Unannotated: "as_write"}).EffectiveUnannotated(), "explicit value always wins")
}

// TestValidateProfiles_V3Rules pins every fatal/warning message row of
// data-model.md §1 exactly, with PolicyEnforcementReady overridden true
// (the 108-a/108-b test-only override, FR-009a; see
// profiles_rollout_gate_test.go for the gate itself).
func TestValidateProfiles_V3Rules(t *testing.T) {
	defer SetPolicyEnforcementReadyForTest(true)()

	t.Run("invalid max_tier is fatal", func(t *testing.T) {
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, MaxTier: "bogus"}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, `profiles[0]: invalid max_tier "bogus": must be one of`)
	})

	t.Run("invalid unannotated is fatal", func(t *testing.T) {
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Unannotated: "bogus"}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, `profiles[0]: invalid unannotated "bogus": must be one of`)
	})

	t.Run("invalid tool pattern is fatal", func(t *testing.T) {
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Tools: &ProfileToolRules{Allow: []string{"no-colon"}}}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, `profiles[0]: invalid tool pattern "no-colon"`)
	})

	t.Run("invalid classify tier is fatal", func(t *testing.T) {
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Tools: &ProfileToolRules{Classify: map[string]string{"a:tool": "bogus"}}}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, `profiles[0]: invalid classify tier "bogus": must be one of`)
	})

	t.Run("switchable_to self-reference is fatal", func(t *testing.T) {
		self := []string{"prof"}
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, SwitchableTo: &self}}}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "profiles[0]: switchable_to cannot include the profile itself")
	})

	t.Run("title too long is fatal", func(t *testing.T) {
		long := make([]byte, 81)
		for i := range long {
			long[i] = 'x'
		}
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Title: string(long)}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, "profiles[0]: title too long")
	})

	t.Run("description too long is fatal", func(t *testing.T) {
		long := make([]byte, 501)
		for i := range long {
			long[i] = 'x'
		}
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Description: string(long)}}}
		_, err := ValidateProfiles(cfg)
		require.ErrorContains(t, err, "profiles[0]: description too long")
	})

	t.Run("rule naming server outside profile is a warning, entry saved", func(t *testing.T) {
		cfg := &Config{
			Servers:  []*ServerConfig{{Name: "a"}, {Name: "b"}},
			Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, Tools: &ProfileToolRules{Allow: []string{"b:tool"}}}},
		}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Contains(t, warnings, `profile "prof" rule "b:tool" names server "b" outside the profile; ignored`)
	})

	t.Run("switchable_to unknown profile is a warning, entry saved", func(t *testing.T) {
		target := []string{"missing"}
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, SwitchableTo: &target}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Contains(t, warnings, `profile "prof" switchable_to references unknown profile "missing"; ignored`)
	})

	t.Run("anonymous_profile unknown is a warning", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "missing", Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Contains(t, warnings, `anonymous_profile "missing" does not exist; anonymous callers are denied all tools`)
	})

	t.Run("anonymous_profile unknown with zero profiles is still a warning", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "missing"}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Contains(t, warnings, `anonymous_profile "missing" does not exist; anonymous callers are denied all tools`)
	})

	t.Run("known anonymous_profile is not a warning", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "prof", Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.NotContains(t, warnings, `anonymous_profile "prof" does not exist; anonymous callers are denied all tools`)
	})

	t.Run("valid v3 profile has no error", func(t *testing.T) {
		switchTo := []string{"other"}
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, Profiles: []ProfileConfig{
			{Name: "prof", Servers: []string{"a"}, MaxTier: "read", Unannotated: "deny",
				Tools:        &ProfileToolRules{Allow: []string{"a:list"}, Deny: []string{"a:delete*"}, Classify: map[string]string{"a:list": "read"}},
				SwitchableTo: &switchTo},
			{Name: "other", Servers: []string{"a"}},
		}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Empty(t, warnings)
	})
}

// TestValidateProfiles_WiredIntoBothDoors pins FR-007's "one function used
// by config load [and] REST" requirement precisely: ValidateProfiles runs on
// BOTH the boot path (Config.Validate) and the write-surface gate
// (Config.ValidateDetailed), each exactly once, and the boot path's error
// text is the validator's RAW message — never re-wrapped as
// "profiles: <msg>" by a ValidationError (which would also change every
// pre-existing Spec 057 profile error's boot-time text, not only the new v3
// ones).
func TestValidateProfiles_WiredIntoBothDoors(t *testing.T) {
	newInvalidCfg := func() *Config {
		cfg := DefaultConfig()
		cfg.Profiles = []ProfileConfig{{Name: "ALL-CAPS-INVALID"}}
		return cfg
	}

	t.Run("boot path (Validate) returns the raw ValidateProfiles message, unwrapped", func(t *testing.T) {
		cfg := newInvalidCfg()
		_, wantErr := ValidateProfiles(cfg)
		require.Error(t, wantErr)

		err := newInvalidCfg().Validate()
		require.Error(t, err)
		require.Equal(t, wantErr.Error(), err.Error(), "Validate() must surface ValidateProfiles' exact text, not a ValidationError-wrapped copy")
		require.NotContains(t, err.Error(), "profiles: profiles[", "must not double-prefix the field name onto the message")
	})

	t.Run("write-surface gate (ValidateDetailed) rejects the same config", func(t *testing.T) {
		cfg := newInvalidCfg()
		errs := cfg.ValidateDetailed()
		var found *ValidationError
		for i := range errs {
			if errs[i].Field == "profiles" {
				found = &errs[i]
				break
			}
		}
		require.NotNil(t, found, "ValidateDetailed must reject an invalid profile (FR-007: one function used by config load and REST)")
	})

	t.Run("a valid config passes both doors with no profiles error", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Profiles = []ProfileConfig{{Name: "work", Servers: nil}}
		require.NoError(t, cfg.Validate())

		for _, e := range DefaultConfig().ValidateDetailed() {
			require.NotEqual(t, "profiles", e.Field)
		}
	})
}

// TestFieldSet_ProfileConfig is the FR-009 guard (T004): reflection over
// ProfileConfig's JSON tags MUST equal exactly the FR-001 field set. It
// fails on any added field (e.g. an owner/user/team/tenant field), keeping
// the profile object edition-neutral. It runs identically under the default
// and -tags server builds (this file carries no build tag).
func TestFieldSet_ProfileConfig(t *testing.T) {
	want := map[string]bool{
		"name":             true,
		"servers":          true,
		"title":            true,
		"description":      true,
		"max_tier":         true,
		"unannotated":      true,
		"tools":            true,
		"code_execution":   true,
		"management_tools": true,
		"switchable_to":    true,
	}

	typ := reflect.TypeOf(ProfileConfig{})
	got := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := tag
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				name = tag[:j]
				break
			}
		}
		got[name] = true
	}

	require.Equal(t, want, got, "ProfileConfig must remain edition-neutral: exactly the FR-001 field set (FR-009)")
}
