package oauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthFlowCoordinator_StartFlow(t *testing.T) {
	t.Run("first flow starts successfully", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()
		flowCtx, err := coordinator.StartFlow("test-server")

		require.NoError(t, err)
		require.NotNil(t, flowCtx)
		assert.Equal(t, "test-server", flowCtx.ServerName)
		assert.NotEmpty(t, flowCtx.CorrelationID)
		assert.Equal(t, FlowInitiated, flowCtx.State)

		// Clean up
		coordinator.EndFlow("test-server", true, nil)
	})

	t.Run("second flow returns ErrFlowInProgress", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		// Start first flow
		flowCtx1, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)

		// Try to start second flow - should fail
		flowCtx2, err := coordinator.StartFlow("test-server")
		assert.Equal(t, ErrFlowInProgress, err)
		assert.Equal(t, flowCtx1, flowCtx2) // Returns existing flow context

		// Clean up
		coordinator.EndFlow("test-server", true, nil)
	})

	t.Run("different servers can have concurrent flows", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		flowCtx1, err := coordinator.StartFlow("server-1")
		require.NoError(t, err)

		flowCtx2, err := coordinator.StartFlow("server-2")
		require.NoError(t, err)

		assert.NotEqual(t, flowCtx1.CorrelationID, flowCtx2.CorrelationID)

		// Clean up
		coordinator.EndFlow("server-1", true, nil)
		coordinator.EndFlow("server-2", true, nil)
	})
}

func TestOAuthFlowCoordinator_EndFlow(t *testing.T) {
	t.Run("end flow success clears active flow", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		_, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)
		assert.True(t, coordinator.IsFlowActive("test-server"))

		coordinator.EndFlow("test-server", true, nil)
		assert.False(t, coordinator.IsFlowActive("test-server"))
	})

	t.Run("end flow failure clears active flow", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		_, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)

		coordinator.EndFlow("test-server", false, assert.AnError)
		assert.False(t, coordinator.IsFlowActive("test-server"))
	})

	t.Run("can start new flow after ending previous", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		flowCtx1, _ := coordinator.StartFlow("test-server")
		coordinator.EndFlow("test-server", true, nil)

		flowCtx2, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)
		assert.NotEqual(t, flowCtx1.CorrelationID, flowCtx2.CorrelationID)

		coordinator.EndFlow("test-server", true, nil)
	})
}

func TestOAuthFlowCoordinator_WaitForFlow(t *testing.T) {
	t.Run("returns nil when no flow active", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		err := coordinator.WaitForFlow(context.Background(), "test-server", time.Second)
		assert.NoError(t, err)
	})

	t.Run("waits for flow completion", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		_, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)

		// Start waiter in goroutine
		var waitErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitErr = coordinator.WaitForFlow(context.Background(), "test-server", 5*time.Second)
		}()

		// End flow after short delay
		time.Sleep(100 * time.Millisecond)
		coordinator.EndFlow("test-server", true, nil)

		wg.Wait()
		assert.NoError(t, waitErr)
	})

	t.Run("returns timeout error when flow takes too long", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		_, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)

		err = coordinator.WaitForFlow(context.Background(), "test-server", 100*time.Millisecond)
		assert.Equal(t, ErrFlowTimeout, err)

		// Clean up
		coordinator.EndFlow("test-server", false, nil)
	})

	t.Run("returns context error when context cancelled", func(t *testing.T) {
		coordinator := NewOAuthFlowCoordinator()

		_, err := coordinator.StartFlow("test-server")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err = coordinator.WaitForFlow(ctx, "test-server", 5*time.Second)
		assert.ErrorIs(t, err, context.Canceled)

		// Clean up
		coordinator.EndFlow("test-server", false, nil)
	})
}

func TestOAuthFlowCoordinator_IsFlowActive(t *testing.T) {
	coordinator := NewOAuthFlowCoordinator()

	assert.False(t, coordinator.IsFlowActive("test-server"))

	_, _ = coordinator.StartFlow("test-server")
	assert.True(t, coordinator.IsFlowActive("test-server"))

	coordinator.EndFlow("test-server", true, nil)
	assert.False(t, coordinator.IsFlowActive("test-server"))
}

func TestOAuthFlowCoordinator_ConcurrentAccess(t *testing.T) {
	coordinator := NewOAuthFlowCoordinator()
	serverName := "concurrent-test"

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Try to start 10 concurrent flows - only one should succeed
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := coordinator.StartFlow(serverName)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, successCount, "Only one flow should have started")
	assert.True(t, coordinator.IsFlowActive(serverName))

	coordinator.EndFlow(serverName, true, nil)
}

func TestOAuthFlowCoordinator_MultipleWaiters(t *testing.T) {
	coordinator := NewOAuthFlowCoordinator()

	_, err := coordinator.StartFlow("test-server")
	require.NoError(t, err)

	var wg sync.WaitGroup
	waitResults := make([]error, 5)

	// Start multiple waiters
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			waitResults[idx] = coordinator.WaitForFlow(context.Background(), "test-server", 5*time.Second)
		}(i)
	}

	// Give waiters time to register
	time.Sleep(100 * time.Millisecond)

	// End flow - all waiters should be notified
	coordinator.EndFlow("test-server", true, nil)

	wg.Wait()

	// All waiters should succeed
	for i, err := range waitResults {
		assert.NoError(t, err, "Waiter %d should not have error", i)
	}
}

func TestGetGlobalCoordinator(t *testing.T) {
	coord1 := GetGlobalCoordinator()
	coord2 := GetGlobalCoordinator()

	assert.Same(t, coord1, coord2, "GetGlobalCoordinator should return singleton")
}
