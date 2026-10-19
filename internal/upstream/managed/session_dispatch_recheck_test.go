package managed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dirtyRecheck(ctx context.Context, dirty *atomic.Bool, errDirty error) context.Context {
	return WithDispatchRecheck(ctx, func() error {
		if dirty.Load() {
			return errDirty
		}
		return nil
	})
}

// Spec 117 FR-018: a verdict that flips while a session re-init re-lists the
// toolset refuses the post-reinit retry, so the call is never executed.
func TestSessionReinit_DispatchRecheckBeforeRetry(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	up.mu.Lock()
	up.listEntered, up.listRelease = entered, release
	up.mu.Unlock()

	errDirty := errors.New("dirty")
	var dirty atomic.Bool
	ctx := dirtyRecheck(tctx(t), &dirty, errDirty)

	done := make(chan error, 1)
	go func() { _, err := mc.CallTool(ctx, "read_thing", nil); done <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("re-init flight never reached tools/list")
	}
	dirty.Store(true)
	close(release)

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrDispatchRecheckRefused), "got %v", err)
		assert.True(t, errors.Is(err, errDirty), "got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the call never returned")
	}
	inits, _, executed := up.snapshot()
	assert.Equal(t, 2, inits, "the re-init ran")
	assert.Zero(t, executed["read_thing"], "the refused retry is never sent")
}

// Spec 117 FR-018: a caller that waits in joinGap on another call's re-init
// flight re-reads its verdict before its first send.
func TestSessionReinit_DispatchRecheckAfterJoinGap(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	up.mu.Lock()
	up.listEntered, up.listRelease = entered, release
	up.mu.Unlock()

	base := tctx(t)
	first := make(chan error, 1)
	go func() { _, err := mc.CallTool(base, "read_thing", nil); first <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("re-init flight never reached tools/list")
	}

	errDirty := errors.New("dirty")
	var dirty atomic.Bool
	ctx := dirtyRecheck(base, &dirty, errDirty)
	second := make(chan error, 1)
	go func() { _, err := mc.CallTool(ctx, "read_thing", nil); second <- err }()
	time.Sleep(300 * time.Millisecond) // the second caller is parked in joinGap
	dirty.Store(true)
	close(release)

	require.NoError(t, <-first)
	select {
	case err := <-second:
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrDispatchRecheckRefused), "got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting call never returned")
	}
	_, _, executed := up.snapshot()
	assert.Equal(t, 1, executed["read_thing"], "only the clean caller executed")
}
