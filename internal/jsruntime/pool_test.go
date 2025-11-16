package jsruntime

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestNewPool tests pool creation
func TestNewPool(t *testing.T) {
	pool, err := NewPool(5)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	if pool.Size() != 5 {
		t.Errorf("expected pool size 5, got %d", pool.Size())
	}

	if pool.Available() != 5 {
		t.Errorf("expected 5 available instances, got %d", pool.Available())
	}
}

// TestNewPoolInvalidSize tests pool creation with invalid size
func TestNewPoolInvalidSize(t *testing.T) {
	_, err := NewPool(0)
	if err == nil {
		t.Errorf("expected error for pool size 0, got nil")
	}

	_, err = NewPool(-1)
	if err == nil {
		t.Errorf("expected error for negative pool size, got nil")
	}
}

// TestPoolAcquireRelease tests basic acquire and release
func TestPoolAcquireRelease(t *testing.T) {
	pool, err := NewPool(3)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Acquire an instance
	vm, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire instance: %v", err)
	}

	if pool.Available() != 2 {
		t.Errorf("expected 2 available after acquire, got %d", pool.Available())
	}

	// Release the instance
	err = pool.Release(vm)
	if err != nil {
		t.Fatalf("failed to release instance: %v", err)
	}

	if pool.Available() != 3 {
		t.Errorf("expected 3 available after release, got %d", pool.Available())
	}
}

// TestPoolConcurrentAcquire tests concurrent acquisition
func TestPoolConcurrentAcquire(t *testing.T) {
	pool, err := NewPool(10)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	numGoroutines := 50
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	// Acquire and release concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			vm, err := pool.Acquire(ctx)
			if err != nil {
				errors <- err
				return
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			err = pool.Release(vm)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("concurrent operation failed: %v", err)
	}

	// All instances should be back in the pool
	if pool.Available() != 10 {
		t.Errorf("expected 10 available after concurrent operations, got %d", pool.Available())
	}
}

// TestPoolAcquireBlocking tests that Acquire blocks when pool is empty
func TestPoolAcquireBlocking(t *testing.T) {
	pool, err := NewPool(1)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Acquire the only instance
	vm, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire instance: %v", err)
	}

	// Try to acquire another instance with timeout
	acquireDone := make(chan bool)
	go func() {
		ctx2, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := pool.Acquire(ctx2)
		if err == nil {
			t.Errorf("expected timeout error, got nil")
		}
		acquireDone <- true
	}()

	// Wait for the blocking acquire to timeout
	<-acquireDone

	// Release the instance
	err = pool.Release(vm)
	if err != nil {
		t.Fatalf("failed to release instance: %v", err)
	}

	// Now acquire should succeed immediately
	vm2, err := pool.Acquire(ctx)
	if err != nil {
		t.Errorf("expected successful acquire after release, got error: %v", err)
	}
	pool.Release(vm2)
}

// TestPoolClose tests pool closure
func TestPoolClose(t *testing.T) {
	pool, err := NewPool(3)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	err = pool.Close()
	if err != nil {
		t.Errorf("failed to close pool: %v", err)
	}

	// Acquiring from closed pool should fail
	ctx := context.Background()
	_, err = pool.Acquire(ctx)
	if err == nil {
		t.Errorf("expected error when acquiring from closed pool, got nil")
	}

	// Closing again should return error
	err = pool.Close()
	if err == nil {
		t.Errorf("expected error when closing already closed pool, got nil")
	}
}

// TestPoolResize tests pool resizing
func TestPoolResize(t *testing.T) {
	pool, err := NewPool(5)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Grow the pool
	err = pool.Resize(10)
	if err != nil {
		t.Fatalf("failed to resize pool to 10: %v", err)
	}

	if pool.Size() != 10 {
		t.Errorf("expected pool size 10 after resize, got %d", pool.Size())
	}

	// Shrink the pool
	err = pool.Resize(3)
	if err != nil {
		t.Fatalf("failed to resize pool to 3: %v", err)
	}

	if pool.Size() != 3 {
		t.Errorf("expected pool size 3 after resize, got %d", pool.Size())
	}

	// Invalid resize should fail
	err = pool.Resize(0)
	if err == nil {
		t.Errorf("expected error for resize to 0, got nil")
	}
}

// TestPoolWithExecute tests using the pool with actual JavaScript execution
func TestPoolWithExecute(t *testing.T) {
	pool, err := NewPool(3)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	caller := newMockToolCaller()

	// Execute multiple scripts concurrently
	numExecutions := 10
	var wg sync.WaitGroup
	results := make(chan *Result, numExecutions)

	for i := 0; i < numExecutions; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			code := `({ index: input.index, result: input.index * 2 })`
			opts := ExecutionOptions{
				Input: map[string]interface{}{
					"index": index,
				},
			}

			// Note: We're not actually using the pool here yet - this is just testing
			// that concurrent Execute calls work. In production, the server would
			// use pool.Acquire() before calling Execute()
			result := Execute(ctx, caller, code, opts)
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)

	// Verify all executions succeeded
	successCount := 0
	for result := range results {
		if result.Ok {
			successCount++
		} else {
			t.Errorf("execution failed: %v", result.Error)
		}
	}

	if successCount != numExecutions {
		t.Errorf("expected %d successful executions, got %d", numExecutions, successCount)
	}
}
