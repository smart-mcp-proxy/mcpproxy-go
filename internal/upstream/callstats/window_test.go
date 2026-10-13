package callstats

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func TestWindow_CountsAndExpiry(t *testing.T) {
	w := &Window{}
	for i := 0; i < 6; i++ {
		w.Record(t0, true, true, KindNetwork)
	}
	for i := 0; i < 4; i++ {
		w.Record(t0, true, false, KindNone)
	}
	calls, failures, dominant := w.Snapshot(t0)
	assert.Equal(t, 10, calls)
	assert.Equal(t, 6, failures)
	assert.Equal(t, KindNetwork, dominant)

	// Still inside the window just before 5 minutes elapse.
	calls, failures, _ = w.Snapshot(t0.Add(WindowSize - BucketSize))
	assert.Equal(t, 10, calls)
	assert.Equal(t, 6, failures)

	// Gone once the bucket falls out of the window.
	calls, failures, dominant = w.Snapshot(t0.Add(WindowSize + BucketSize))
	assert.Equal(t, 0, calls)
	assert.Equal(t, 0, failures)
	assert.Equal(t, KindNone, dominant)
}

func TestWindow_BucketRotationResetsStaleBucket(t *testing.T) {
	w := &Window{}
	w.Record(t0, true, true, KindTimeout)
	// Same slot, exactly one full window later: the stale bucket must be reset,
	// not accumulated into.
	later := t0.Add(WindowSize)
	w.Record(later, true, false, KindNone)
	calls, failures, _ := w.Snapshot(later)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 0, failures)
}

func TestWindow_UncountedIsIgnored(t *testing.T) {
	w := &Window{}
	w.Record(t0, false, false, KindNone)
	w.Record(t0, false, true, KindNetwork)
	calls, failures, _ := w.Snapshot(t0)
	assert.Equal(t, 0, calls)
	assert.Equal(t, 0, failures)
}

func TestWindow_DominantKind(t *testing.T) {
	w := &Window{}
	w.Record(t0, true, true, KindHTTP)
	w.Record(t0, true, true, KindTimeout)
	w.Record(t0.Add(30*time.Second), true, true, KindTimeout)
	_, _, dominant := w.Snapshot(t0.Add(time.Minute))
	assert.Equal(t, KindTimeout, dominant)
}

func TestWindow_ConcurrentRecord(t *testing.T) {
	w := &Window{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				w.Record(t0, true, i%2 == 0, KindNetwork)
				w.Snapshot(t0)
			}
		}()
	}
	wg.Wait()
	calls, failures, _ := w.Snapshot(t0)
	assert.Equal(t, 2000, calls)
	assert.Equal(t, 1000, failures)
}

func TestRegistry_RecordSnapshotDrop(t *testing.T) {
	now := t0
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })

	calls, failures, _ := r.Snapshot("unknown")
	assert.Equal(t, 0, calls)
	assert.Equal(t, 0, failures)

	for i := 0; i < 3; i++ {
		r.Record("a", true, true, KindHTTP)
	}
	r.Record("b", true, false, KindNone)
	calls, failures, dominant := r.Snapshot("a")
	require.Equal(t, 3, calls)
	assert.Equal(t, 3, failures)
	assert.Equal(t, KindHTTP, dominant)

	r.Drop("a")
	calls, _, _ = r.Snapshot("a")
	assert.Equal(t, 0, calls)
	calls, _, _ = r.Snapshot("b")
	assert.Equal(t, 1, calls)

	now = now.Add(2 * WindowSize)
	calls, _, _ = r.Snapshot("b")
	assert.Equal(t, 0, calls)
}
