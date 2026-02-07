package flow

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCorrelator_RegisterAndMatch tests basic register + match + consume flow.
func TestCorrelator_RegisterAndMatch(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	argsHash := HashContent("github:get_file" + `{"path":"README.md"}`)

	c.RegisterPending("hook-session-1", argsHash, "github:get_file")

	// Match should return the hook session ID
	hookSessionID := c.MatchAndConsume(argsHash)
	assert.Equal(t, "hook-session-1", hookSessionID)
}

// TestCorrelator_MatchReturnsEmptyForUnknownHash tests that unknown hashes return empty.
func TestCorrelator_MatchReturnsEmptyForUnknownHash(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	hookSessionID := c.MatchAndConsume("nonexistent-hash")
	assert.Empty(t, hookSessionID, "unknown hash should return empty string")
}

// TestCorrelator_ConsumedEntriesDeleted tests that matched entries are consumed (no double-match).
func TestCorrelator_ConsumedEntriesDeleted(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	argsHash := HashContent("slack:send_message" + `{"text":"hello"}`)
	c.RegisterPending("hook-session-2", argsHash, "slack:send_message")

	// First match succeeds
	result1 := c.MatchAndConsume(argsHash)
	assert.Equal(t, "hook-session-2", result1)

	// Second match should fail (consumed)
	result2 := c.MatchAndConsume(argsHash)
	assert.Empty(t, result2, "consumed entry should not match again")
}

// TestCorrelator_TTLExpiry tests that pending entries expire after TTL.
func TestCorrelator_TTLExpiry(t *testing.T) {
	// Use a very short TTL for testing
	c := NewCorrelator(50 * time.Millisecond)
	defer c.Stop()

	argsHash := HashContent("postgres:query" + `{"sql":"SELECT 1"}`)
	c.RegisterPending("hook-session-3", argsHash, "postgres:query")

	// Should match immediately
	assert.Equal(t, "hook-session-3", c.MatchAndConsume(argsHash))

	// Re-register and wait for expiry
	c.RegisterPending("hook-session-4", argsHash, "postgres:query")
	time.Sleep(100 * time.Millisecond)

	// Should not match after TTL
	result := c.MatchAndConsume(argsHash)
	assert.Empty(t, result, "expired entry should not match")
}

// TestCorrelator_MultipleSessionsIsolated tests that multiple sessions don't cross-contaminate.
func TestCorrelator_MultipleSessionsIsolated(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	hash1 := HashContent("tool1" + `{"a":"1"}`)
	hash2 := HashContent("tool2" + `{"b":"2"}`)

	c.RegisterPending("session-A", hash1, "tool1")
	c.RegisterPending("session-B", hash2, "tool2")

	// Each hash matches its own session
	assert.Equal(t, "session-A", c.MatchAndConsume(hash1))
	assert.Equal(t, "session-B", c.MatchAndConsume(hash2))

	// Neither should match again
	assert.Empty(t, c.MatchAndConsume(hash1))
	assert.Empty(t, c.MatchAndConsume(hash2))
}

// TestCorrelator_ConcurrentSafety tests concurrent RegisterPending + MatchAndConsume.
func TestCorrelator_ConcurrentSafety(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	const goroutines = 50
	var wg sync.WaitGroup

	// Register entries from multiple goroutines
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hash := HashContent(fmt.Sprintf("tool-%d-args-%d", idx, idx))
			c.RegisterPending(fmt.Sprintf("session-%d", idx), hash, fmt.Sprintf("tool-%d", idx))
		}(i)
	}
	wg.Wait()

	// Match from multiple goroutines — each should match exactly once
	results := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hash := HashContent(fmt.Sprintf("tool-%d-args-%d", idx, idx))
			results[idx] = c.MatchAndConsume(hash)
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		assert.Equal(t, fmt.Sprintf("session-%d", i), results[i],
			"goroutine %d should match its own session", i)
	}
}

// TestCorrelator_OverwritesPreviousPending tests that re-registering the same hash
// overwrites the previous pending entry.
func TestCorrelator_OverwritesPreviousPending(t *testing.T) {
	c := NewCorrelator(5 * time.Second)
	defer c.Stop()

	argsHash := HashContent("tool:action" + `{"key":"val"}`)

	c.RegisterPending("old-session", argsHash, "tool:action")
	c.RegisterPending("new-session", argsHash, "tool:action")

	// Should return the newest session
	result := c.MatchAndConsume(argsHash)
	assert.Equal(t, "new-session", result)
}
