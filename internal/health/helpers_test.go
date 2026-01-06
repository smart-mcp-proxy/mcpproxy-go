package health

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"mcpproxy-go/internal/contracts"
)

func TestIsHealthy_WithHealthyLevel(t *testing.T) {
	health := &contracts.HealthStatus{
		Level:      LevelHealthy,
		AdminState: StateEnabled,
		Summary:    "Connected (5 tools)",
	}

	assert.True(t, IsHealthy(health, false), "should return true when health.level is healthy")
	assert.True(t, IsHealthy(health, true), "should return true when health.level is healthy, ignoring legacy")
}

func TestIsHealthy_WithDegradedLevel(t *testing.T) {
	health := &contracts.HealthStatus{
		Level:      LevelDegraded,
		AdminState: StateEnabled,
		Summary:    "Token expiring soon",
	}

	assert.False(t, IsHealthy(health, false), "should return false when health.level is degraded")
	assert.False(t, IsHealthy(health, true), "should return false when health.level is degraded, ignoring legacy")
}

func TestIsHealthy_WithUnhealthyLevel(t *testing.T) {
	health := &contracts.HealthStatus{
		Level:      LevelUnhealthy,
		AdminState: StateEnabled,
		Summary:    "Connection error",
	}

	assert.False(t, IsHealthy(health, false), "should return false when health.level is unhealthy")
	assert.False(t, IsHealthy(health, true), "should return false when health.level is unhealthy, ignoring legacy")
}

func TestIsHealthy_NilHealthFallsBackToLegacy(t *testing.T) {
	// When health is nil, should fall back to legacy connected field
	assert.True(t, IsHealthy(nil, true), "should return legacy connected value when health is nil")
	assert.False(t, IsHealthy(nil, false), "should return legacy connected value when health is nil")
}

func TestIsHealthy_DisabledServerIsHealthy(t *testing.T) {
	// Disabled servers are intentionally not running, so they're considered "healthy"
	// (admin made a deliberate choice)
	health := &contracts.HealthStatus{
		Level:      LevelHealthy,
		AdminState: StateDisabled,
		Summary:    "Disabled",
	}

	assert.True(t, IsHealthy(health, false), "disabled servers should be considered healthy")
}

func TestIsHealthy_QuarantinedServerIsHealthy(t *testing.T) {
	// Quarantined servers are intentionally blocked, so they're considered "healthy"
	// (admin made a deliberate choice)
	health := &contracts.HealthStatus{
		Level:      LevelHealthy,
		AdminState: StateQuarantined,
		Summary:    "Quarantined for review",
	}

	assert.True(t, IsHealthy(health, false), "quarantined servers should be considered healthy")
}
