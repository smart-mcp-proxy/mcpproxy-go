package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// seedStreamRecords writes n tool-call records so the stream has more than
// the channel buffer (100) left to send after one is consumed.
func seedStreamRecords(t *testing.T, m *Manager, n int) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < n; i++ {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			ID:         fmt.Sprintf("stream-cancel-%04d", i),
			Type:       ActivityTypeToolCall,
			ServerName: "srv",
			ToolName:   "tool",
			Status:     ActivityStatusSuccess,
			Timestamp:  base.Add(time.Duration(i) * time.Millisecond),
		}))
	}
}

// newStreamTestManager opens a manager in a temp dir without registering
// Close as cleanup: a failing assertion means a stream still pins a read
// transaction, and Close would then hang the whole package run.
func newStreamTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	return m
}

func waitForOpenTx(t *testing.T, m *Manager, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m.GetDB().Stats().OpenTxN == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("OpenTxN = %d, want %d after 5s", m.GetDB().Stats().OpenTxN, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func closeWithin(t *testing.T, m *Manager, d time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(d):
		t.Fatalf("Manager.Close did not return within %s: a stream still holds a read transaction", d)
	}
}

// A consumer that stops reading after the first record and cancels its
// context must not pin the producer's BBolt read transaction: the producer
// returns from db.View, OpenTxN drops to 0 and Close does not hang.
func TestStreamActivities_CancelReleasesReadTransaction(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 300)

	ctx, cancel := context.WithCancel(context.Background())
	ch := manager.StreamActivities(ctx, ActivityFilter{Types: []string{string(ActivityTypeToolCall)}})

	first, ok := <-ch
	require.True(t, ok)
	require.NotNil(t, first)
	cancel() // walk away with >100 records left to send

	waitForOpenTx(t, manager, 0)
	// The channel is closed once the producer gives up, so a late reader
	// cannot block on it.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				closeWithin(t, manager, 5*time.Second)
				return
			}
		case <-deadline:
			t.Fatal("stream channel was not closed after cancellation")
		}
	}
}

// A consumer that walks away WITHOUT cancelling still leaks the transaction
// (that is the contract: the context is the release). This guards the test
// above against passing vacuously if the buffer ever grows past the seed.
func TestStreamActivities_AbandonedWithoutCancelHoldsTransaction(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 300)

	ctx, cancel := context.WithCancel(context.Background())
	ch := manager.StreamActivities(ctx, ActivityFilter{Types: []string{string(ActivityTypeToolCall)}})
	_, ok := <-ch
	require.True(t, ok)

	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 1, manager.GetDB().Stats().OpenTxN, "producer should be parked on a full channel inside db.View")

	cancel()
	waitForOpenTx(t, manager, 0)
	closeWithin(t, manager, 5*time.Second)
}

// Cancelling before the first read yields no records and a closed channel.
func TestStreamActivities_CancelledContextYieldsNothing(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := manager.StreamActivities(ctx, ActivityFilter{})
	n := 0
	for range ch {
		n++
	}
	require.Equal(t, 0, n, "a done context is checked before every send")
	waitForOpenTx(t, manager, 0)
	closeWithin(t, manager, 5*time.Second)
}

// The full stream still delivers everything when the context stays live.
func TestStreamActivities_LiveContextStreamsAll(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 250)

	n := 0
	for range manager.StreamActivities(context.Background(), ActivityFilter{Types: []string{string(ActivityTypeToolCall)}}) {
		n++
	}
	require.Equal(t, 250, n)
	waitForOpenTx(t, manager, 0)
	closeWithin(t, manager, 5*time.Second)
}
