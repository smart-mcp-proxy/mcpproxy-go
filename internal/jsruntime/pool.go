package jsruntime

import (
	"context"
	"fmt"
	"sync"

	"github.com/dop251/goja"
)

// Pool manages a pool of reusable JavaScript runtime instances for concurrent execution
type Pool struct {
	size      int
	available chan *goja.Runtime
	mu        sync.Mutex
	closed    bool
}

// NewPool creates a new JavaScript runtime pool with the specified size
func NewPool(size int) (*Pool, error) {
	if size < 1 {
		return nil, fmt.Errorf("pool size must be at least 1, got %d", size)
	}

	pool := &Pool{
		size:      size,
		available: make(chan *goja.Runtime, size),
		closed:    false,
	}

	// Pre-allocate runtime instances
	for i := 0; i < size; i++ {
		vm := goja.New()
		pool.available <- vm
	}

	return pool, nil
}

// Acquire obtains a runtime instance from the pool
// Blocks until an instance is available or context is cancelled
func (p *Pool) Acquire(ctx context.Context) (*goja.Runtime, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool is closed")
	}
	p.mu.Unlock()

	select {
	case vm := <-p.available:
		return vm, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns a runtime instance to the pool
func (p *Pool) Release(vm *goja.Runtime) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("pool is closed")
	}

	// Reset the runtime to clean state for reuse
	// Note: Goja doesn't have a reset method, so we create a new instance
	newVM := goja.New()

	select {
	case p.available <- newVM:
		return nil
	default:
		// This shouldn't happen if Release is called correctly
		return fmt.Errorf("pool is full (possible double-release)")
	}
}

// Size returns the configured pool size
func (p *Pool) Size() int {
	return p.size
}

// Available returns the number of available instances in the pool
func (p *Pool) Available() int {
	return len(p.available)
}

// Close closes the pool and releases all resources
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("pool already closed")
	}

	p.closed = true
	close(p.available)

	// Drain the channel
	for range p.available {
		// VMs will be garbage collected
	}

	return nil
}

// Resize adjusts the pool size (for hot configuration reload)
// If newSize > current size, adds new instances
// If newSize < current size, instances will be removed as they're released
func (p *Pool) Resize(newSize int) error {
	if newSize < 1 {
		return fmt.Errorf("pool size must be at least 1, got %d", newSize)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("pool is closed")
	}

	if newSize == p.size {
		return nil // No change needed
	}

	// Create new channel with new size
	newAvailable := make(chan *goja.Runtime, newSize)

	// Drain existing channel and transfer instances
	oldAvailable := p.available
	p.available = newAvailable

	instanceCount := 0
drainLoop:
	for {
		select {
		case vm := <-oldAvailable:
			instanceCount++
			// Only keep instances up to newSize
			if instanceCount <= newSize {
				newAvailable <- vm
			}
			// Extra instances beyond newSize are discarded
		default:
			// Channel is empty
			break drainLoop
		}
	}

	// If growing, add new instances to reach newSize
	if newSize > instanceCount {
		diff := newSize - instanceCount
		for i := 0; i < diff; i++ {
			vm := goja.New()
			newAvailable <- vm
		}
	}

	p.size = newSize
	return nil
}
