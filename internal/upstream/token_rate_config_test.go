package upstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// TestAgentTokenRateLimit_FollowsTheConfig: agent_token_rate_limit reaches the
// limiter registry at construction and on every hot reload, and removing the
// block switches the default off again (issue #1699).
func TestAgentTokenRateLimit_FollowsTheConfig(t *testing.T) {
	t.Setenv("CI", "")

	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	rates := m.Limiters().TokenRates()
	assert.False(t, rates.Default().Enabled(), "no config, no limit")

	m.SetGlobalConfig(&config.Config{AgentTokenRateLimit: &config.AgentTokenRateLimit{PerMinute: 60, Burst: 10}})
	assert.Equal(t, limiter.RateSpec{PerMinute: 60, Burst: 10}, rates.Default())

	m.SetGlobalConfig(&config.Config{AgentTokenRateLimit: &config.AgentTokenRateLimit{PerMinute: 120}})
	assert.Equal(t, limiter.RateSpec{PerMinute: 120}, rates.Default())

	m.SetGlobalConfig(&config.Config{AgentTokenRateLimit: &config.AgentTokenRateLimit{}})
	assert.False(t, rates.Default().Enabled(), "per_minute 0 is off")

	m.SetGlobalConfig(&config.Config{AgentTokenRateLimit: &config.AgentTokenRateLimit{PerMinute: 5}})
	m.SetGlobalConfig(&config.Config{})
	assert.False(t, rates.Default().Enabled(), "removing the block turns the default off")

	m.SetGlobalConfig(nil)
	assert.False(t, rates.Default().Enabled())
}

func TestNewManager_AppliesTheConfiguredAgentTokenRateLimit(t *testing.T) {
	t.Setenv("CI", "")

	cfg := &config.Config{AgentTokenRateLimit: &config.AgentTokenRateLimit{PerMinute: 30, Burst: 3}}
	m := NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	assert.Equal(t, limiter.RateSpec{PerMinute: 30, Burst: 3}, m.Limiters().TokenRates().Default())
}
