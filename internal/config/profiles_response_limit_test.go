package config

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Issue #2027: a profile may override the global tool_response_limit. Unset
// inherits, 0 means no limit, a positive value replaces the global one.
func TestProfileEffectiveToolResponseLimit(t *testing.T) {
	require.Equal(t, 20000, ProfileConfig{}.EffectiveToolResponseLimit(20000), "unset inherits the global limit")
	require.Equal(t, 0, ProfileConfig{ToolResponseLimit: intPtr(0)}.EffectiveToolResponseLimit(20000), "0 means no limit")
	require.Equal(t, 500000, ProfileConfig{ToolResponseLimit: intPtr(500000)}.EffectiveToolResponseLimit(20000))
	require.Equal(t, 100, ProfileConfig{ToolResponseLimit: intPtr(100)}.EffectiveToolResponseLimit(0), "a profile may also bound an unlimited global")
}

func TestValidateProfiles_ToolResponseLimit(t *testing.T) {
	cfg := &Config{
		Servers:  []*ServerConfig{{Name: "a"}},
		Profiles: []ProfileConfig{{Name: "script", Servers: []string{"a"}, ToolResponseLimit: intPtr(-1)}},
	}
	_, err := ValidateProfilesDetailed(cfg)
	var verr *ProfileValidationError
	require.True(t, errors.As(err, &verr), "a negative limit is refused")
	require.Equal(t, "tool_response_limit", verr.Field)
	require.Equal(t, 0, verr.Index)

	for _, ok := range []int{0, 1, 20000, 5000000} {
		cfg.Profiles[0].ToolResponseLimit = intPtr(ok)
		_, err := ValidateProfilesDetailed(cfg)
		require.NoError(t, err, "limit %d", ok)
	}
}

// The limit is a delivery setting, not policy: setting it must not make a
// profile non-legacy (which would switch on retrieve_tools' hidden_by_profile).
func TestProfileToolResponseLimit_NotPolicy(t *testing.T) {
	p := ProfileConfig{Name: "script", ToolResponseLimit: intPtr(0)}
	require.True(t, p.IsLegacy())
	_, set := firstSetPolicyField(p)
	require.False(t, set)
}

// An explicit 0 must survive a JSON round trip (omitempty on a plain int
// would drop it and silently restore the global limit).
func TestProfileToolResponseLimit_JSONRoundTrip(t *testing.T) {
	b, err := json.Marshal(ProfileConfig{Name: "script", Servers: []string{"a"}, ToolResponseLimit: intPtr(0)})
	require.NoError(t, err)
	require.Contains(t, string(b), `"tool_response_limit":0`)
	var back ProfileConfig
	require.NoError(t, json.Unmarshal(b, &back))
	require.NotNil(t, back.ToolResponseLimit)
	require.Equal(t, 0, *back.ToolResponseLimit)

	b, err = json.Marshal(ProfileConfig{Name: "plain", Servers: []string{"a"}})
	require.NoError(t, err)
	require.NotContains(t, string(b), "tool_response_limit")
}
