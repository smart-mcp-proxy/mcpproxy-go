package runtime

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// toolCall builds a minimal tool_call ActivityRecord for aggregate tests.
func toolCall(server, tool, status string, durationMs int64, reqBytes, respBytes int, ts time.Time) *storage.ActivityRecord {
	return &storage.ActivityRecord{
		Type:          storage.ActivityTypeToolCall,
		ServerName:    server,
		ToolName:      tool,
		Status:        status,
		DurationMs:    durationMs,
		RequestBytes:  reqBytes,
		ResponseBytes: respBytes,
		Timestamp:     ts,
	}
}

func TestUsageAggregate_Apply_CountsAndBytes(t *testing.T) {
	agg := newUsageAggregate()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// 3 successes, 1 error, 1 blocked on github:search.
	agg.Apply(toolCall("github", "search", "success", 100, 200, 1000, base))
	agg.Apply(toolCall("github", "search", "success", 100, 200, 2000, base))
	agg.Apply(toolCall("github", "search", "success", 100, 0, 0, base)) // legacy 0-byte
	agg.Apply(toolCall("github", "search", "error", 100, 200, 500, base))
	agg.Apply(toolCall("github", "search", "blocked", 100, 200, 0, base)) // resp unknown

	tu := agg.Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.Equal(t, "github", tu.Server)
	assert.Equal(t, "search", tu.Tool)
	assert.Equal(t, int64(5), tu.Calls)
	assert.Equal(t, int64(1), tu.Errors)
	assert.Equal(t, int64(1), tu.Blocked)

	// Byte sums exclude 0-byte records.
	assert.Equal(t, int64(1000+2000+500), tu.RespBytesSum)
	assert.Equal(t, int64(3), tu.SizedRespCalls, "3 records had ResponseBytes>0")
	assert.Equal(t, int64(200*4), tu.ReqBytesSum, "4 records had RequestBytes>0")
	assert.Equal(t, int64(4), tu.SizedReqCalls)
}

func TestUsageAggregate_Apply_IgnoresNonToolCalls(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// Non tool_call records and empty tool names are ignored.
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypePolicyDecision, ServerName: "x", ToolName: "y", Status: "blocked", Timestamp: ts})
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeToolCall, ServerName: "x", ToolName: "", Status: "success", Timestamp: ts})

	assert.Empty(t, agg.Tools)
}

func TestToolUsage_Averages_ExcludeZeroByteCalls(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg.Apply(toolCall("s", "t", "success", 10, 0, 1000, ts)) // sized resp
	agg.Apply(toolCall("s", "t", "success", 10, 0, 3000, ts)) // sized resp
	agg.Apply(toolCall("s", "t", "success", 10, 0, 0, ts))    // legacy, excluded

	tu := agg.Tools[toolKey("s", "t")]
	avg, ok := tu.AvgRespBytes()
	require.True(t, ok)
	assert.Equal(t, int64(2000), avg, "(1000+3000)/2, excluding the 0-byte call")

	// No sized request calls -> average not available.
	_, ok = tu.AvgReqBytes()
	assert.False(t, ok)
}

func TestToolUsage_Percentile_FromLatencyBuckets(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	// 100 calls: 90 fast (~30ms), 10 slow (~3000ms).
	for i := 0; i < 90; i++ {
		agg.Apply(toolCall("s", "t", "success", 30, 0, 100, ts))
	}
	for i := 0; i < 10; i++ {
		agg.Apply(toolCall("s", "t", "success", 3000, 0, 100, ts))
	}
	tu := agg.Tools[toolKey("s", "t")]

	p50 := tu.Percentile(0.50)
	p95 := tu.Percentile(0.95)
	// p50 sits in the fast band, p95 must reflect the slow tail.
	assert.LessOrEqual(t, p50, int64(50), "p50 ~ fast band")
	assert.Greater(t, p95, int64(1000), "p95 must capture the slow tail")
	assert.GreaterOrEqual(t, p95, p50)
}

func TestUsageAggregate_TimeBuckets_PerHour(t *testing.T) {
	agg := newUsageAggregate()
	h10 := time.Date(2026, 6, 1, 10, 5, 0, 0, time.UTC)
	h10b := time.Date(2026, 6, 1, 10, 47, 0, 0, time.UTC)
	h11 := time.Date(2026, 6, 1, 11, 2, 0, 0, time.UTC)

	agg.Apply(toolCall("s", "t", "success", 10, 0, 100, h10))
	agg.Apply(toolCall("s", "t", "error", 10, 0, 200, h10b)) // same hour bucket as h10
	agg.Apply(toolCall("s", "t", "success", 10, 0, 300, h11))

	buckets := agg.Timeline()
	require.Len(t, buckets, 2, "two distinct hourly buckets")

	// Buckets returned in chronological order, hour-aligned.
	assert.Equal(t, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), buckets[0].Start)
	assert.Equal(t, int64(2), buckets[0].Calls)
	assert.Equal(t, int64(1), buckets[0].Errors)
	assert.Equal(t, int64(300), buckets[0].RespBytesSum)

	assert.Equal(t, time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC), buckets[1].Start)
	assert.Equal(t, int64(1), buckets[1].Calls)
}

func TestUsageAggregate_LastUsed_TracksLatest(t *testing.T) {
	agg := newUsageAggregate()
	early := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	agg.Apply(toolCall("s", "t", "success", 10, 0, 100, late))
	agg.Apply(toolCall("s", "t", "success", 10, 0, 100, early))
	tu := agg.Tools[toolKey("s", "t")]
	assert.Equal(t, late, tu.LastUsed)
}

func TestUsageAggregate_Clone_IsDeepCopy(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))

	clone := agg.clone()
	// Mutating the original after cloning must not affect the clone.
	agg.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))

	assert.Equal(t, int64(2), agg.Tools[toolKey("s", "t")].Calls)
	assert.Equal(t, int64(1), clone.Tools[toolKey("s", "t")].Calls, "clone must be independent")
}

// TestUsageStore_SnapshotReflectsWrites_ReadsNeverBlock validates the actor
// ownership contract (T007): the writer applies records; readers see an
// immutable snapshot via atomic pointer with no blocking.
func TestUsageStore_SnapshotReflectsWrites_ReadsNeverBlock(t *testing.T) {
	store := newUsageStore()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// Empty snapshot is available immediately (never nil).
	require.NotNil(t, store.Snapshot())
	assert.Empty(t, store.Snapshot().Tools)

	// Concurrent readers hammer Snapshot() while a single writer applies.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = store.Snapshot() // must never block or panic
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		store.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))
	}
	close(stop)
	wg.Wait()

	snap := store.Snapshot()
	require.NotNil(t, snap.Tools[toolKey("s", "t")])
	assert.Equal(t, int64(200), snap.Tools[toolKey("s", "t")].Calls)
}

func TestUsageStore_Replace_PublishesNewSnapshot(t *testing.T) {
	store := newUsageStore()
	rebuilt := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	rebuilt.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))

	store.Replace(rebuilt)
	assert.Equal(t, int64(1), store.Snapshot().Tools[toolKey("s", "t")].Calls)
}

// TestUsageStore_ApplyDoesNotPublishPerWrite is the spec-069 hot-path contract
// (MCP-835): Apply must be O(1) and must NOT clone/publish the aggregate on every
// activity write. The O(tools×buckets) clone is deferred until a reader actually
// reads the snapshot, so a burst of writes triggers zero publishes.
func TestUsageStore_ApplyDoesNotPublishPerWrite(t *testing.T) {
	store := newUsageStore()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	base := store.publishes.Load() // 1 from construction
	const N = 500
	for i := 0; i < N; i++ {
		store.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))
	}
	// No reader has called Snapshot since the writes: zero new clones/publishes.
	assert.Equal(t, base, store.publishes.Load(),
		"Apply must not clone/publish on the activity hot path")

	// The next read materializes exactly one snapshot reflecting all writes.
	snap := store.Snapshot()
	require.NotNil(t, snap.Tools[toolKey("s", "t")])
	assert.Equal(t, int64(N), snap.Tools[toolKey("s", "t")].Calls)
	assert.Equal(t, base+1, store.publishes.Load(), "exactly one publish on first read")

	// A second read with no intervening write must reuse the clean snapshot
	// (lock-free fast path), not re-clone.
	_ = store.Snapshot()
	assert.Equal(t, base+1, store.publishes.Load(),
		"clean reads must not re-clone")
}

// BenchmarkUsageStore_Apply primes the aggregate with many distinct tools so a
// per-write clone would be O(tools)-expensive, then benchmarks Apply. After the
// MCP-835 fix, allocs/op is a small constant independent of the primed size
// (the per-write clone is gone); regress this if allocs/op scales with priming.
func BenchmarkUsageStore_Apply(b *testing.B) {
	store := newUsageStore()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		store.Apply(toolCall(fmt.Sprintf("srv%04d", i), "t", "success", 10, 0, 100, ts))
	}
	_ = store.Snapshot() // publish once so the primed state is materialized

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Apply(toolCall("s", "t", "success", 10, 0, 100, ts))
	}
}
