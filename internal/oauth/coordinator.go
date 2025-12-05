// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements OAuth flow coordination to prevent race conditions.
package oauth

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Default timeouts for OAuth flow coordination.
const (
	// DefaultFlowTimeout is the maximum time to wait for an OAuth flow to complete.
	DefaultFlowTimeout = 5 * time.Minute
	// StaleFlowTimeout is the time after which a flow is considered stale and can be cleared.
	StaleFlowTimeout = 10 * time.Minute
)

// ErrFlowTimeout indicates that waiting for an OAuth flow timed out.
var ErrFlowTimeout = errors.New("timeout waiting for OAuth flow to complete")

// ErrFlowInProgress indicates an OAuth flow is already in progress for the server.
var ErrFlowInProgress = errors.New("OAuth flow already in progress")

// flowWaiter represents a goroutine waiting for an OAuth flow to complete.
type flowWaiter struct {
	done   chan struct{}
	result error
}

// OAuthFlowCoordinator coordinates OAuth flows to ensure only one flow runs per server.
// This prevents race conditions where multiple reconnection attempts trigger concurrent OAuth flows.
type OAuthFlowCoordinator struct {
	// activeFlows tracks the active OAuth flow context for each server.
	activeFlows map[string]*OAuthFlowContext
	// flowLocks provides per-server mutexes to serialize OAuth operations.
	flowLocks map[string]*sync.Mutex
	// waiters tracks goroutines waiting for a flow to complete.
	waiters map[string][]*flowWaiter
	// mu protects all map operations.
	mu sync.RWMutex
	// logger for coordinator operations.
	logger *zap.Logger
}

// globalCoordinator is the singleton OAuth flow coordinator.
var globalCoordinator *OAuthFlowCoordinator
var coordinatorOnce sync.Once

// GetGlobalCoordinator returns the global OAuth flow coordinator instance.
func GetGlobalCoordinator() *OAuthFlowCoordinator {
	coordinatorOnce.Do(func() {
		globalCoordinator = NewOAuthFlowCoordinator()
	})
	return globalCoordinator
}

// NewOAuthFlowCoordinator creates a new OAuth flow coordinator.
func NewOAuthFlowCoordinator() *OAuthFlowCoordinator {
	return &OAuthFlowCoordinator{
		activeFlows: make(map[string]*OAuthFlowContext),
		flowLocks:   make(map[string]*sync.Mutex),
		waiters:     make(map[string][]*flowWaiter),
		logger:      zap.L().Named("oauth-coordinator"),
	}
}

// getOrCreateLock returns the mutex for the given server, creating one if needed.
func (c *OAuthFlowCoordinator) getOrCreateLock(serverName string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()

	if lock, exists := c.flowLocks[serverName]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	c.flowLocks[serverName] = lock
	return lock
}

// StartFlow starts a new OAuth flow for the given server.
// If a flow is already in progress, returns the existing flow context and ErrFlowInProgress.
// The caller should use WaitForFlow() instead if they want to wait for the existing flow.
func (c *OAuthFlowCoordinator) StartFlow(serverName string) (*OAuthFlowContext, error) {
	lock := c.getOrCreateLock(serverName)
	lock.Lock()
	defer lock.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if a flow is already active
	if existingFlow, exists := c.activeFlows[serverName]; exists {
		// Check if the flow is stale
		if time.Since(existingFlow.StartTime) > StaleFlowTimeout {
			c.logger.Warn("Clearing stale OAuth flow",
				zap.String("server", serverName),
				zap.String("correlation_id", existingFlow.CorrelationID),
				zap.Duration("age", time.Since(existingFlow.StartTime)),
			)
			// Clear stale flow and proceed
			delete(c.activeFlows, serverName)
		} else {
			c.logger.Info("OAuth flow already in progress",
				zap.String("server", serverName),
				zap.String("existing_correlation_id", existingFlow.CorrelationID),
				zap.String("state", existingFlow.State.String()),
			)
			return existingFlow, ErrFlowInProgress
		}
	}

	// Create new flow context
	flowCtx := NewOAuthFlowContext(serverName)
	c.activeFlows[serverName] = flowCtx

	c.logger.Info("Started new OAuth flow",
		zap.String("server", serverName),
		zap.String("correlation_id", flowCtx.CorrelationID),
	)

	return flowCtx, nil
}

// EndFlow marks an OAuth flow as completed (success or failure).
// This notifies any waiting goroutines and cleans up the flow state.
func (c *OAuthFlowCoordinator) EndFlow(serverName string, success bool, err error) {
	lock := c.getOrCreateLock(serverName)
	lock.Lock()
	defer lock.Unlock()

	c.mu.Lock()
	flowCtx := c.activeFlows[serverName]
	waiters := c.waiters[serverName]

	// Update flow state
	if flowCtx != nil {
		if success {
			flowCtx.State = FlowCompleted
		} else {
			flowCtx.State = FlowFailed
		}

		LogOAuthFlowEnd(c.logger, serverName, flowCtx.CorrelationID, success, flowCtx.Duration())
	}

	// Clean up
	delete(c.activeFlows, serverName)
	delete(c.waiters, serverName)
	c.mu.Unlock()

	// Notify all waiters
	for _, waiter := range waiters {
		waiter.result = err
		close(waiter.done)
	}
}

// IsFlowActive checks if an OAuth flow is currently active for the given server.
func (c *OAuthFlowCoordinator) IsFlowActive(serverName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	flow, exists := c.activeFlows[serverName]
	if !exists {
		return false
	}

	// Check if the flow is stale
	if time.Since(flow.StartTime) > StaleFlowTimeout {
		return false
	}

	return true
}

// GetActiveFlow returns the active OAuth flow context for the given server, if any.
func (c *OAuthFlowCoordinator) GetActiveFlow(serverName string) *OAuthFlowContext {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.activeFlows[serverName]
}

// WaitForFlow waits for an active OAuth flow to complete.
// Returns nil if no flow is active (caller should start one).
// Returns ErrFlowTimeout if the wait times out.
// Returns the flow's error if it failed.
func (c *OAuthFlowCoordinator) WaitForFlow(ctx context.Context, serverName string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = DefaultFlowTimeout
	}

	c.mu.Lock()
	flow, exists := c.activeFlows[serverName]
	if !exists {
		c.mu.Unlock()
		return nil // No flow to wait for
	}

	// Create a waiter
	waiter := &flowWaiter{
		done: make(chan struct{}),
	}
	c.waiters[serverName] = append(c.waiters[serverName], waiter)
	c.mu.Unlock()

	c.logger.Info("Waiting for OAuth flow to complete",
		zap.String("server", serverName),
		zap.String("flow_correlation_id", flow.CorrelationID),
		zap.Duration("timeout", timeout),
	)

	// Wait for flow completion or timeout
	select {
	case <-waiter.done:
		return waiter.result
	case <-time.After(timeout):
		c.logger.Warn("Timeout waiting for OAuth flow",
			zap.String("server", serverName),
			zap.Duration("timeout", timeout),
		)
		return ErrFlowTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// UpdateFlowState updates the state of an active OAuth flow.
func (c *OAuthFlowCoordinator) UpdateFlowState(serverName string, state OAuthFlowState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if flow, exists := c.activeFlows[serverName]; exists {
		flow.State = state
		c.logger.Debug("Updated OAuth flow state",
			zap.String("server", serverName),
			zap.String("correlation_id", flow.CorrelationID),
			zap.String("new_state", state.String()),
		)
	}
}

// CleanupStaleFlows removes any OAuth flows that have been running longer than StaleFlowTimeout.
// This should be called periodically to prevent memory leaks from abandoned flows.
func (c *OAuthFlowCoordinator) CleanupStaleFlows() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleaned := 0
	now := time.Now()

	for serverName, flow := range c.activeFlows {
		if now.Sub(flow.StartTime) > StaleFlowTimeout {
			c.logger.Warn("Cleaning up stale OAuth flow",
				zap.String("server", serverName),
				zap.String("correlation_id", flow.CorrelationID),
				zap.Duration("age", now.Sub(flow.StartTime)),
			)

			// Notify any waiters with timeout error
			for _, waiter := range c.waiters[serverName] {
				waiter.result = ErrFlowTimeout
				close(waiter.done)
			}

			delete(c.activeFlows, serverName)
			delete(c.waiters, serverName)
			cleaned++
		}
	}

	return cleaned
}
