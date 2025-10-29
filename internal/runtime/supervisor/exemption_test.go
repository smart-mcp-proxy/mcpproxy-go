package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestCircuitBreaker tests the circuit breaker pattern for inspection failures
func TestCircuitBreaker(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Test failure recording
	s := &Supervisor{
		logger:             logger,
		inspectionFailures: make(map[string]*inspectionFailureInfo),
	}

	serverName := "test-server"

	// Record 3 failures
	for i := 0; i < 3; i++ {
		s.RecordInspectionFailure(serverName)
	}

	// Check that circuit breaker is now active
	allowed, reason, cooldown := s.CanInspect(serverName)
	assert.False(t, allowed, "Should not allow inspection after 3 failures")
	assert.Contains(t, reason, "Circuit breaker active")
	assert.Greater(t, cooldown, time.Duration(0), "Should have cooldown remaining")

	// Verify cooldown duration is approximately 5 minutes
	assert.InDelta(t, inspectionCooldown.Seconds(), cooldown.Seconds(), 1.0, "Cooldown should be ~5 minutes")
}

// TestCircuitBreakerReset tests that successful inspection resets the failure counter
func TestCircuitBreakerReset(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	s := &Supervisor{
		logger:             logger,
		inspectionFailures: make(map[string]*inspectionFailureInfo),
	}

	serverName := "test-server"

	// Record 2 failures
	s.RecordInspectionFailure(serverName)
	s.RecordInspectionFailure(serverName)

	// Verify we're still allowed (under threshold)
	allowed, _, _ := s.CanInspect(serverName)
	assert.True(t, allowed, "Should still allow inspection with 2 failures")

	// Record success - should reset counter
	s.RecordInspectionSuccess(serverName)

	// Verify failure counter was reset
	failures, inCooldown, _ := s.GetInspectionStats(serverName)
	assert.Equal(t, 0, failures, "Failure counter should be reset after success")
	assert.False(t, inCooldown, "Should not be in cooldown after success")
}

// TestExemptionExpiry tests that exemptions expire correctly
func TestExemptionExpiry(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	s := &Supervisor{
		logger:                logger,
		inspectionExemptions:  make(map[string]time.Time),
		inspectionFailures:    make(map[string]*inspectionFailureInfo),
	}

	serverName := "test-server"

	// Grant exemption with short duration
	s.inspectionExemptions[serverName] = time.Now().Add(100 * time.Millisecond)

	// Should be exempted immediately
	assert.True(t, s.IsInspectionExempted(serverName), "Should be exempted immediately")

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Should no longer be exempted
	assert.False(t, s.IsInspectionExempted(serverName), "Should not be exempted after expiry")
}
