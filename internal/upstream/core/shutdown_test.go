//go:build unix

package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestShutdownTimeoutConstants(t *testing.T) {
	// Verify timeout relationship: processGracefulTimeout < mcpClientCloseTimeout
	// This ensures killProcessGroup can complete within the MCP close timeout
	assert.Less(t, processGracefulTimeout, mcpClientCloseTimeout,
		"processGracefulTimeout (%v) must be less than mcpClientCloseTimeout (%v)",
		processGracefulTimeout, mcpClientCloseTimeout)

	// Verify reasonable values
	assert.Equal(t, 10*time.Second, mcpClientCloseTimeout,
		"mcpClientCloseTimeout should be 10 seconds")
	assert.Equal(t, 9*time.Second, processGracefulTimeout,
		"processGracefulTimeout should be 9 seconds")
	assert.Equal(t, 100*time.Millisecond, processTerminationPollInterval,
		"processTerminationPollInterval should be 100ms")
}

func TestKillProcessGroup_AlreadyDead(t *testing.T) {
	// killProcessGroup should return quickly when process group doesn't exist
	logger := zap.NewNop()

	// Use a PGID that definitely doesn't exist (negative or very large)
	nonExistentPGID := 999999999

	start := time.Now()
	err := killProcessGroup(nonExistentPGID, logger, "test-server")
	elapsed := time.Since(start)

	// Should return without error (process already dead is not an error)
	assert.NoError(t, err)

	// Should return quickly (not wait for full timeout)
	assert.Less(t, elapsed, 1*time.Second,
		"killProcessGroup should return quickly for non-existent process group, took %v", elapsed)
}

func TestKillProcessGroup_InvalidPGID(t *testing.T) {
	logger := zap.NewNop()

	// Zero PGID should return immediately
	start := time.Now()
	err := killProcessGroup(0, logger, "test-server")
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond,
		"killProcessGroup with PGID=0 should return immediately")

	// Negative PGID should return immediately
	start = time.Now()
	err = killProcessGroup(-1, logger, "test-server")
	elapsed = time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond,
		"killProcessGroup with negative PGID should return immediately")
}
