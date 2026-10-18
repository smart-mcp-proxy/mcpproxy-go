package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A re-initialize waits for requests already on the client and gives up at its
// own deadline instead of hanging behind a long in-flight call.
func TestSessionGate_ExclusiveWaitsForSharedAndHonoursContext(t *testing.T) {
	var g sessionGate
	release, err := g.acquireShared(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = g.acquireExclusive(ctx)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)

	release()
	excl, err := g.acquireExclusive(context.Background())
	require.NoError(t, err)
	excl()
}

// A request queued behind a re-initialize returns when its caller goes away,
// and is admitted once the re-initialize releases the gate.
func TestSessionGate_SharedQueuesBehindExclusive(t *testing.T) {
	c := &Client{}
	g := &c.sessionGate
	excl, err := g.acquireExclusive(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = c.beginRequest(ctx, "tools/call")
	require.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)

	admitted := make(chan struct{})
	go func() {
		rel, err := g.acquireShared(context.Background())
		if err == nil {
			rel()
		}
		close(admitted)
	}()
	select {
	case <-admitted:
		t.Fatal("request admitted while a re-initialize holds the gate")
	case <-time.After(50 * time.Millisecond):
	}
	excl()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("request not admitted after the re-initialize released the gate")
	}
}
