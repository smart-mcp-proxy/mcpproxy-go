package server

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// TestUpstreamToolCallerIsConcurrencySafe (Spec 096): call_tools() dispatches
// its elements from a worker pool, so the adapter the sandbox calls is now
// entered concurrently. Every call must still produce exactly one record and
// the type must be race-clean.
//
// Per-server admission (Spec 093) is enforced deeper, inside
// managed.Client.CallTool, and is covered by that spec's tests — nothing here
// re-tests it. That the workers dispatch under the execution's context (rather
// than a fresh background one) is pinned in internal/jsruntime/batch_test.go,
// where a stub ToolCaller can capture what it was handed.
func TestUpstreamToolCallerIsConcurrencySafe(t *testing.T) {
	const callers = 32

	logger := zap.NewNop()
	// No clients are registered, so every call takes the "server not found"
	// branch: it exercises the recording path without needing a live upstream.
	upstreamManager := upstream.NewManager(logger, &config.Config{}, nil, nil, nil)

	caller := &upstreamToolCaller{
		upstreamManager: upstreamManager,
		logger:          logger,
		executionID:     "exec-1",
	}

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := caller.CallTool(context.Background(), "missing", "tool", map[string]interface{}{})
			assert.Error(t, err)
		}()
	}
	wg.Wait()

	require.Len(t, caller.getToolCalls(), callers, "every concurrent dispatch must record exactly one call")
}
