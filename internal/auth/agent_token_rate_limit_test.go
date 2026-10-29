package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1699: the per-token call budget is an additive, omitempty field.

func TestTokenRateLimit_Validate(t *testing.T) {
	var none *TokenRateLimit
	assert.NoError(t, none.Validate(), "no budget of its own is valid")

	valid := []TokenRateLimit{
		{PerMinute: 0},
		{PerMinute: 1},
		{PerMinute: 60, Burst: 10},
		{PerMinute: 1, Burst: 100},
		{PerMinute: MaxRateLimitPerMinute, Burst: MaxRateLimitPerMinute},
	}
	for _, rl := range valid {
		rl := rl
		assert.NoError(t, rl.Validate(), "%+v", rl)
	}

	invalid := []struct {
		rl   TokenRateLimit
		want string
	}{
		{TokenRateLimit{PerMinute: -1}, "per_minute"},
		{TokenRateLimit{PerMinute: 5, Burst: -1}, "burst"},
		{TokenRateLimit{PerMinute: MaxRateLimitPerMinute + 1}, "per_minute"},
		{TokenRateLimit{PerMinute: 5, Burst: MaxRateLimitPerMinute + 1}, "burst"},
		{TokenRateLimit{PerMinute: 0, Burst: 3}, "burst"},
	}
	for _, tc := range invalid {
		tc := tc
		err := tc.rl.Validate()
		require.Error(t, err, "%+v", tc.rl)
		assert.Contains(t, err.Error(), tc.want, "%+v", tc.rl)
	}
}

func TestAgentToken_RateLimitRoundTripAndOmitEmpty(t *testing.T) {
	plain := AgentToken{Name: "plain"}
	raw, err := json.Marshal(plain)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "rate_limit", "records written before this field existed stay unchanged")

	limited := AgentToken{Name: "limited", RateLimit: &TokenRateLimit{PerMinute: 60, Burst: 10}}
	raw, err = json.Marshal(limited)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"rate_limit":{"per_minute":60,"burst":10}`)

	var back AgentToken
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NotNil(t, back.RateLimit)
	assert.Equal(t, *limited.RateLimit, *back.RateLimit)

	// An exemption keeps its explicit zero: it is not the same as "absent".
	exempt := AgentToken{Name: "exempt", RateLimit: &TokenRateLimit{PerMinute: 0}}
	raw, err = json.Marshal(exempt)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"rate_limit":{"per_minute":0}`)
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.NotNil(t, back.RateLimit)
}

func TestAgentToken_AuthContextCarriesACopyOfTheRateLimit(t *testing.T) {
	assert.Nil(t, (&AgentToken{Name: "plain"}).AuthContext().RateLimit)

	tok := &AgentToken{Name: "limited", RateLimit: &TokenRateLimit{PerMinute: 60, Burst: 10}}
	ac := tok.AuthContext()
	require.NotNil(t, ac.RateLimit)
	assert.Equal(t, *tok.RateLimit, *ac.RateLimit)

	ac.RateLimit.PerMinute = 1
	assert.Equal(t, 60, tok.RateLimit.PerMinute, "changing the request context must not change the token record")
}

func TestAdminAndAnonymousContextsHaveNoRateLimit(t *testing.T) {
	assert.Nil(t, AdminContext().RateLimit)
	assert.Nil(t, AnonymousContext().RateLimit)
}
