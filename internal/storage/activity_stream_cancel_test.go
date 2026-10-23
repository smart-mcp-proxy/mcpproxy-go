package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
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

// A consumer that stops reading WITHOUT cancelling (an export to an HTTP
// client that keeps the connection open but no longer reads) must not pin a
// read transaction either: the producer parks on the channel send outside
// db.View (#1742).
func TestStreamActivities_StalledConsumerHoldsNoTransaction(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 300)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := manager.StreamActivities(ctx, ActivityFilter{Types: []string{string(ActivityTypeToolCall)}})
	_, ok := <-ch
	require.True(t, ok)

	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, manager.GetDB().Stats().OpenTxN, "producer must not hold a read transaction while blocked on a send")

	cancel()
	closeWithin(t, manager, 5*time.Second)
}

// The #1742 hang: a stalled stream plus a write that must grow (remap) the
// file. With the read transaction held across the send, the writer waits for
// the mmap lock for ever and every later reader queues behind it.
func TestStreamActivities_StalledConsumerDoesNotBlockRemap(t *testing.T) {
	manager := newStreamTestManager(t)
	seedStreamRecords(t, manager, 300)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := manager.StreamActivities(ctx, ActivityFilter{Types: []string{string(ActivityTypeToolCall)}})
	_, ok := <-ch
	require.True(t, ok)
	time.Sleep(100 * time.Millisecond) // let the producer fill the buffer and park

	db := manager.GetDB()
	grow := make([]byte, 16<<20) // larger than the current mmap: forces a remap
	done := make(chan error, 1)
	go func() {
		done <- db.Update(func(tx *bbolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte("remap-probe"))
			if err != nil {
				return err
			}
			return b.Put([]byte("k"), grow)
		})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		cancel() // unwedge the writer so the package run can finish
		<-done
		t.Fatal("a write needing a remap blocked behind a stalled activity stream")
	}

	// The stream resumes where it stopped and loses nothing across batches.
	n := 1
	for range ch {
		n++
	}
	require.Equal(t, 300, n, "stream must deliver every record across batches")
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

// Offset, limit and order hold across batch boundaries, and a filter that
// matches rarely still finds every match past the per-transaction scan cap.
func TestStreamActivities_BatchBoundaries(t *testing.T) {
	manager := newStreamTestManager(t)
	const total = 2500 // > streamScanPerTx, > several streamBatchSize
	seedStreamRecords(t, manager, total)

	collect := func(f ActivityFilter) []string {
		var ids []string
		for rec := range manager.StreamActivities(context.Background(), f) {
			ids = append(ids, rec.ID)
		}
		return ids
	}
	id := func(i int) string { return fmt.Sprintf("stream-cancel-%04d", i) }

	all := collect(ActivityFilter{})
	require.Len(t, all, total)
	for i, got := range all {
		require.Equal(t, id(total-1-i), got, "newest first, no gaps or repeats")
	}

	page := collect(ActivityFilter{Offset: 150, Limit: 230})
	require.Len(t, page, 230)
	require.Equal(t, id(total-1-150), page[0])
	require.Equal(t, id(total-1-150-229), page[229])

	// One match near each end of a 2500-key walk: the scan cap must not end
	// the stream early.
	sparse := collect(ActivityFilter{Types: []string{string(ActivityTypeToolCall)}, RequestID: "none"})
	require.Empty(t, sparse)
	require.NoError(t, manager.SaveActivity(&ActivityRecord{
		ID: "oldest-match", Type: ActivityTypeToolCall, ServerName: "rare", ToolName: "t",
		Status: ActivityStatusSuccess, Timestamp: time.Now().UTC().Add(-2 * time.Hour),
	}))
	rare := collect(ActivityFilter{Server: "rare"})
	require.Equal(t, []string{"oldest-match"}, rare)

	waitForOpenTx(t, manager, 0)
	closeWithin(t, manager, 5*time.Second)
}
