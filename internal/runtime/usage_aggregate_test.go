package runtime

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
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
	// A blocked tool_call never executed (a policy gate refused the dispatch),
	// so like a blocked policy_decision it takes only the Blocked counter and
	// a failed timeline bar — not Calls, latency or byte statistics.
	assert.Equal(t, int64(4), tu.Calls)
	assert.Equal(t, int64(1), tu.Errors)
	assert.Equal(t, int64(1), tu.Blocked)

	// Byte sums exclude 0-byte records and the refused dispatch.
	assert.Equal(t, int64(1000+2000+500), tu.RespBytesSum)
	assert.Equal(t, int64(3), tu.SizedRespCalls, "3 records had ResponseBytes>0")
	assert.Equal(t, int64(200*3), tu.ReqBytesSum, "3 executed records had RequestBytes>0")
	assert.Equal(t, int64(3), tu.SizedReqCalls)
}

func TestUsageAggregate_Apply_IgnoresNonToolCalls(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// Non-blocked policy decisions and tool_calls with empty tool names are
	// ignored. (Blocked policy decisions ARE counted — see the test below.)
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypePolicyDecision, ServerName: "x", ToolName: "y", Status: "approved", Timestamp: ts})
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeToolCall, ServerName: "x", ToolName: "", Status: "success", Timestamp: ts})

	assert.Empty(t, agg.Tools)
}

// TestUsageAggregate_Apply_CountsBlockedPolicyDecisions (MCP-835 / Codex
// finding #2): blocked tool attempts are persisted as policy_decision records,
// not tool_calls. The aggregate must still count them so the contract's
// per-tool `blocked` field is non-zero. A blocked attempt never executed, so in
// the PER-TOOL rollup it contributes ONLY to Blocked (and LastUsed) — not
// Calls, latency or bytes.
//
// It does enter the TIMELINE, and as an error: the timeline has to match the
// glance the user is looking at, and there a blocked attempt is a red row. A
// run that policy refused wholesale used to render as an empty histogram under
// a list full of failures.
func TestUsageAggregate_Apply_CountsBlockedPolicyDecisions(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypePolicyDecision, ServerName: "github", ToolName: "search", Status: "blocked", Timestamp: ts})
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypePolicyDecision, ServerName: "github", ToolName: "search", Status: "blocked", Timestamp: ts.Add(time.Minute)})

	tu := agg.Tools[toolKey("github", "search")]
	require.NotNil(t, tu, "blocked attempts must create a per-tool entry")
	assert.Equal(t, int64(2), tu.Blocked, "both blocked attempts counted")
	assert.Equal(t, int64(0), tu.Calls, "blocked attempts are not executed calls")
	assert.Equal(t, int64(0), tu.Errors)
	assert.Equal(t, ts.Add(time.Minute), tu.LastUsed, "LastUsed tracks the latest attempt")

	var latencyTotal int64
	for _, c := range tu.LatencyBuckets {
		latencyTotal += c
	}
	assert.Equal(t, int64(0), latencyTotal, "blocked attempts have no latency sample")

	timeline := agg.Timeline()
	require.Len(t, timeline, 1, "both attempts fall in the same hourly bucket")
	assert.Equal(t, int64(2), timeline[0].Calls, "a blocked attempt is a row the user saw")
	assert.Equal(t, int64(2), timeline[0].Errors, "and it is a FAILED row")
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

	p50, p50Exceeds := tu.Percentile(0.50)
	p95, p95Exceeds := tu.Percentile(0.95)
	// p50 sits in the fast band, p95 must reflect the slow tail.
	assert.LessOrEqual(t, p50, int64(50), "p50 ~ fast band")
	assert.Greater(t, p95, int64(1000), "p95 must capture the slow tail")
	assert.GreaterOrEqual(t, p95, p50)
	assert.False(t, p50Exceeds, "a bounded bucket reads as an upper bound")
	assert.False(t, p95Exceeds, "3000ms is inside the histogram, not past its end")
}

// A sub-10ms tool must not report 10ms. Every local stdio server lives in this
// band, and before the low end of latencyBucketBoundsMs was refined, every row
// of the Usage latency table read exactly "10 ms" while the Activity Log showed
// 3/4/5 ms for the same calls (audit finding F22, #1046).
func TestToolUsage_Percentile_ResolvesSubTenMilliseconds(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		agg.Apply(toolCall("s", "t", "success", 3, 0, 100, ts))
	}
	tu := agg.Tools[toolKey("s", "t")]

	p50, exceeds := tu.Percentile(0.50)
	assert.Equal(t, int64(5), p50, "3ms calls bound at 5ms, not at 10ms")
	assert.False(t, exceeds)
}

// The overflow bucket is unbounded: its value is a FLOOR, and saying so is the
// difference between "≤ 10 s" (false) and "> 10 s" (true).
func TestToolUsage_Percentile_OverflowReportsExceeds(t *testing.T) {
	agg := newUsageAggregate()
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		agg.Apply(toolCall("s", "t", "success", 45_000, 0, 100, ts))
	}
	tu := agg.Tools[toolKey("s", "t")]

	p95, exceeds := tu.Percentile(0.95)
	assert.Equal(t, int64(10000), p95, "the last bound is all the histogram knows")
	assert.True(t, exceeds, "the true latency is above that bound, not below it")
}

// An empty histogram has no bound to report either way.
func TestToolUsage_Percentile_EmptyHistogram(t *testing.T) {
	tu := &ToolUsage{LatencyBuckets: make([]int64, numLatencyBuckets())}
	ms, exceeds := tu.Percentile(0.95)
	assert.Equal(t, int64(0), ms)
	assert.False(t, exceeds)
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

// A record whose stored text holds MORE than the agent consumed must not
// contribute its pre-cut size to delivered traffic.
//
// That condition is contracts.CutShortenedAgentOnly and nothing else. It is a
// property of the EMITTER, not of the record type: a built-in (retrieve_tools
// above all) records the full response while the agent consumed the cut text,
// whereas an ordinary upstream tool_call records the POST-forward text and so
// holds the agent's own copy — and a code-execution sub-call, also a tool_call,
// holds a PREFIX of what the sandbox got. Only the first overstates.
//
// This became reachable only when internal calls started carrying byte counts at
// all; before that they contributed 0 and the question could not arise.
func TestUsageAggregate_AgentOnlyCutDoesNotInflateDeliveredBytes(t *testing.T) {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	internal := func(cut contracts.ResponseCut, respBytes int) *storage.ActivityRecord {
		return &storage.ActivityRecord{
			Type:                  storage.ActivityTypeInternalToolCall,
			ToolName:              "retrieve_tools",
			Status:                "success",
			ResponseBytes:         respBytes,
			ResponseTruncated:     cut.Cuts(),
			ResponseTruncationCut: cut,
			Timestamp:             base,
		}
	}
	sum := func(rec *storage.ActivityRecord) int64 {
		agg := newUsageAggregate()
		agg.Apply(rec)
		var total int64
		for _, b := range agg.Buckets {
			total += b.RespBytesSum
		}
		return total
	}

	require.EqualValues(t, 4_000, sum(internal(contracts.CutNone, 4_000)),
		"an untruncated built-in is delivered in full")

	assert.Zero(t, sum(internal(contracts.CutShortenedAgentOnly, 1_000_000)),
		"the stored size describes more than the agent consumed; counting it overstates traffic")

	// An ordinary upstream tool_call is the OPPOSITE case and must still count.
	up := toolCall("github", "search", "success", 10, 100, 50_000, base)
	up.ResponseTruncated = true
	up.ResponseTruncationCut = contracts.CutShortenedAgentAndRecord
	assert.EqualValues(t, 50_000, sum(up),
		"an upstream response was consumed whole up to the forward cut, and the record holds that same copy")
}

// The round-4 regression at the usage aggregate.
//
// A code-execution sub-call is a Type=tool_call record with
// response_truncated=true, and its stamp is CutShortenedRecordOnly: the whole
// response reached the sandbox and only the LOG copy was cut. Its ResponseBytes
// is therefore an honest delivered size and must be counted. A predicate that
// asked "is this truncated and internal?" got this right by accident; one that
// asked "is this truncated?" would silently drop it.
func TestUsageAggregate_RecordOnlyCutStillCountsDeliveredBytes(t *testing.T) {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	sub := toolCall("github", "search", "success", 10, 100, 50_000, base)
	sub.Source = storage.ActivitySourceInternal
	sub.ParentID = "parent-req-1"
	sub.ResponseTruncated = true
	sub.ResponseTruncationCut = contracts.CutShortenedRecordOnly

	agg := newUsageAggregate()
	agg.Apply(sub)
	var total int64
	for _, b := range agg.Buckets {
		total += b.RespBytesSum
	}
	assert.EqualValues(t, 50_000, total,
		"the sandbox received the whole response; only the activity log was cut")
}

// A record from an older core carries no stamp, and the predicate falls back to
// the type test it used to be — which was correct for the emitter population
// that wrote those records. Deleting the fallback would silently start counting
// every legacy retrieve_tools row.
func TestUsageAggregate_UnstampedLegacyRecordsFallBackToTheTypeTest(t *testing.T) {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	sum := func(rec *storage.ActivityRecord) int64 {
		agg := newUsageAggregate()
		agg.Apply(rec)
		var total int64
		for _, b := range agg.Buckets {
			total += b.RespBytesSum
		}
		return total
	}

	// unstamped-legacy-record-on-purpose: written before the stamp existed.
	legacyBuiltin := &storage.ActivityRecord{
		Type:              storage.ActivityTypeInternalToolCall,
		ToolName:          "retrieve_tools",
		Status:            storage.ActivityStatusSuccess,
		ResponseBytes:     1_000_000,
		ResponseTruncated: true,
		Timestamp:         base,
	}
	assert.Zero(t, sum(legacyBuiltin),
		"a legacy truncated built-in still overstates delivered traffic")

	// unstamped-legacy-record-on-purpose: the tool_call half of the same era.
	legacyUpstream := toolCall("github", "search", "success", 10, 100, 50_000, base)
	legacyUpstream.ResponseTruncated = true
	assert.EqualValues(t, 50_000, sum(legacyUpstream),
		"a legacy truncated tool_call held the agent's copy and must still count")
}

// The mirror of the tests above, and the guard on the field split.
//
// A STORAGE-truncated built-in with NO response cut points the other way: the
// log holds LESS than the agent consumed, because activity_max_response_size cut
// the text on the way into BBolt while nothing cut it on the way out.
// ResponseBytes is measured pre-truncation by the emitter, so here it is exactly
// what was delivered and must still be counted. Folding storage truncation into
// ResponseTruncated would send this record through
// truncatedBuiltinOverstatesDelivery and silently under-report — in the
// flattering direction, on the very records the cap creates.
//
// ToolName must be retrieve_tools (or describe_tool): storage.CountsAsCall
// admits a SUCCESSFUL internal_tool_call into the timeline only for those two
// discovery built-ins, so any other name creates no bucket and the assertion
// would be unreachable in both directions.
func TestUsageAggregate_StorageTruncatedBuiltinStillCountsDeliveredBytes(t *testing.T) {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	builtin := func(mutate func(*storage.ActivityRecord)) *storage.ActivityRecord {
		rec := &storage.ActivityRecord{
			Type:          storage.ActivityTypeInternalToolCall,
			ToolName:      "retrieve_tools",
			Status:        storage.ActivityStatusSuccess,
			ResponseBytes: 200_000,
			Timestamp:     base,
		}
		mutate(rec)
		return rec
	}
	sum := func(rec *storage.ActivityRecord) int64 {
		agg := newUsageAggregate()
		agg.Apply(rec)
		var total int64
		for _, b := range agg.Buckets {
			total += b.RespBytesSum
		}
		return total
	}

	// Premise: an untruncated record of this shape DOES reach a bucket, so a
	// zero below means the predicate excluded it rather than that nothing was
	// ever counted.
	require.EqualValues(t, 200_000, sum(builtin(func(*storage.ActivityRecord) {})),
		"fixture must be admissible to the timeline, or both halves prove nothing")

	assert.EqualValues(t, 200_000, sum(builtin(func(r *storage.ActivityRecord) {
		r.ResponseStorageTruncated = true
	})), "the agent received all 200000 bytes; only the stored copy was cut")

	// The negative half: the agent-only direction must STILL be excluded, so
	// this cannot pass by the predicate having been deleted outright.
	assert.Zero(t, sum(builtin(func(r *storage.ActivityRecord) {
		r.ResponseTruncated = true
		r.ResponseTruncationCut = contracts.CutShortenedAgentOnly
	})), "a built-in whose record holds more than was delivered still overstates traffic")
}
