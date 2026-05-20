package flow

import (
	"sync"
	"time"
)

// pendingEntry stores a pending correlation waiting for an MCP call match.
type pendingEntry struct {
	HookSessionID string
	ToolName      string
	Timestamp     time.Time
}

// Correlator links hook sessions to MCP sessions via argument hash matching.
// When an agent hook fires PreToolUse for mcp__mcpproxy__call_tool_*, the inner
// tool name + args are hashed and registered as pending. When the MCP proxy
// receives a matching call, MatchAndConsume returns the hook session ID so the
// sessions can be linked.
type Correlator struct {
	ttl     time.Duration
	pending sync.Map // argsHash → *pendingEntry
	stopCh  chan struct{}
}

// NewCorrelator creates a Correlator with the given TTL for pending entries.
func NewCorrelator(ttl time.Duration) *Correlator {
	c := &Correlator{
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Stop halts the cleanup goroutine.
func (c *Correlator) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// RegisterPending stores a pending correlation keyed by argsHash.
// If an entry with the same hash already exists, it is overwritten.
func (c *Correlator) RegisterPending(hookSessionID, argsHash, toolName string) {
	c.pending.Store(argsHash, &pendingEntry{
		HookSessionID: hookSessionID,
		ToolName:      toolName,
		Timestamp:     time.Now(),
	})
}

// MatchAndConsume looks up and removes a pending entry by argsHash.
// Returns the hook session ID if found and not expired, or empty string otherwise.
func (c *Correlator) MatchAndConsume(argsHash string) string {
	val, ok := c.pending.LoadAndDelete(argsHash)
	if !ok {
		return ""
	}
	entry := val.(*pendingEntry)

	// Check TTL
	if time.Since(entry.Timestamp) > c.ttl {
		return ""
	}
	return entry.HookSessionID
}

// cleanupLoop periodically removes expired pending entries.
func (c *Correlator) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.expireEntries()
		}
	}
}

func (c *Correlator) expireEntries() {
	now := time.Now()
	c.pending.Range(func(key, value any) bool {
		entry := value.(*pendingEntry)
		if now.Sub(entry.Timestamp) > c.ttl {
			c.pending.Delete(key)
		}
		return true
	})
}
