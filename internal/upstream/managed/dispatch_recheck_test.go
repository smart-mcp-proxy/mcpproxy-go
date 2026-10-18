package managed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A WithDispatchRecheck check is read AFTER the admission queue wait: a
// verdict that flips while the call is parked refuses it with zero transport
// calls, wrapping both ErrDispatchRecheckRefused and the check's own error.
func TestDispatchRecheck_VerdictFlipsWhileQueued(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(4),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	mc, reg := newAdmissionClient(t, &config.Config{Servers: []*config.ServerConfig{sc}}, "db", nil)
	fake := &fakeToolCaller{}
	mc.toolInvoker = fake

	errDirty := errors.New("dirty")
	var dirty atomic.Bool
	ctx := WithDispatchRecheck(context.Background(), func() error {
		if dirty.Load() {
			return errDirty
		}
		return nil
	})

	holdSlot, err := mc.acquireAdmission(context.Background(), "hold")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, callErr := mc.CallToolOnEpoch(ctx, "erase", nil, mc.ConnectionEpoch())
		done <- callErr
	}()
	require.Eventually(t, func() bool {
		return reg.Server("db").Stats().Queued == 1
	}, 2*time.Second, 5*time.Millisecond, "the call must be parked in the admission queue")

	dirty.Store(true)
	holdSlot()

	select {
	case callErr := <-done:
		require.Error(t, callErr)
		assert.True(t, errors.Is(callErr, ErrDispatchRecheckRefused), "got %v", callErr)
		assert.True(t, errors.Is(callErr, errDirty), "got %v", callErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the queued call never returned")
	}
	assert.Equal(t, 0, fake.callCount(), "a refused call never reaches the transport")

	// Control: once the verdict clears, the unpinned entry point dispatches.
	dirty.Store(false)
	_, err = mc.CallTool(ctx, "erase", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, fake.callCount())
}

// Nested checks all run: an outer passing check does not mask an inner
// refusal, and vice versa.
func TestDispatchRecheck_NestedChecksAllRun(t *testing.T) {
	errOuter, errInner := errors.New("outer"), errors.New("inner")
	pass := func() error { return nil }

	assert.NoError(t, runDispatchRecheck(context.Background()))
	assert.NoError(t, runDispatchRecheck(WithDispatchRecheck(WithDispatchRecheck(context.Background(), pass), pass)))
	err := runDispatchRecheck(WithDispatchRecheck(WithDispatchRecheck(context.Background(), func() error { return errOuter }), pass))
	assert.True(t, errors.Is(err, errOuter), "got %v", err)
	err = runDispatchRecheck(WithDispatchRecheck(WithDispatchRecheck(context.Background(), pass), func() error { return errInner }))
	assert.True(t, errors.Is(err, errInner), "got %v", err)
	assert.True(t, errors.Is(err, ErrDispatchRecheckRefused))
	assert.Equal(t, context.Background(), WithDispatchRecheck(context.Background(), nil), "a nil check attaches nothing")
}
