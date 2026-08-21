package runtime

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 069 A2: actor-owned usage aggregate.
//
// UsageAggregate is an in-memory rollup of tool-call activity, owned by the
// ActivityService goroutine and mutated incrementally via Apply. Readers never
// touch the live aggregate: the store publishes an immutable deep copy through
// an atomic pointer (copy-on-write), so reads are lock-free and never block.

// usageBucketWidth is the native time-bucket granularity for the timeline.
// Hourly matches the contract example (`start: ...T11:00:00Z`); the endpoint
// (A3) selects the requested window span over these buckets.
const usageBucketWidth = time.Hour

// usageMaxBuckets bounds timeline memory. 24*90 hourly buckets covers the
// default 90-day activity retention; older buckets are evicted oldest-first.
const usageMaxBuckets = 24 * 90

// latencyBucketBoundsMs are the inclusive upper bounds (in ms) of the fixed
// latency histogram buckets. A final overflow bucket captures anything slower
// than the last bound, so there are len(bounds)+1 buckets total.
var latencyBucketBoundsMs = []int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

func numLatencyBuckets() int { return len(latencyBucketBoundsMs) + 1 }

// latencyBucketIndex returns the histogram bucket for a duration in ms.
func latencyBucketIndex(durationMs int64) int {
	for i, bound := range latencyBucketBoundsMs {
		if durationMs <= bound {
			return i
		}
	}
	return len(latencyBucketBoundsMs) // overflow bucket
}

// toolKey builds the per-tool map key. A NUL separator cannot collide with ':'
// or other characters valid in server/tool names (mirrors storage.toolUsageKey).
func toolKey(server, tool string) string {
	return server + "\x00" + tool
}

// ToolUsage is a per-(server,tool) incremental rollup.
type ToolUsage struct {
	Server         string    `json:"server"`
	Tool           string    `json:"tool"`
	Calls          int64     `json:"calls"`
	Errors         int64     `json:"errors"`
	Blocked        int64     `json:"blocked"`
	Rejected       int64     `json:"rejected"` // spec 093: shed by a concurrency limit, never executed
	ReqBytesSum    int64     `json:"req_bytes_sum"`
	RespBytesSum   int64     `json:"resp_bytes_sum"`
	SizedReqCalls  int64     `json:"sized_req_calls"`  // calls with RequestBytes>0
	SizedRespCalls int64     `json:"sized_resp_calls"` // calls with ResponseBytes>0
	LatencyBuckets []int64   `json:"latency_buckets"`  // len == numLatencyBuckets()
	LastUsed       time.Time `json:"last_used"`
}

// AvgRespBytes returns the average response size over sized calls only
// (records with ResponseBytes>0). ok is false when there are no sized calls.
func (t *ToolUsage) AvgRespBytes() (avg int64, ok bool) {
	if t.SizedRespCalls == 0 {
		return 0, false
	}
	return t.RespBytesSum / t.SizedRespCalls, true
}

// AvgReqBytes returns the average request size over sized calls only
// (records with RequestBytes>0). ok is false when there are no sized calls.
func (t *ToolUsage) AvgReqBytes() (avg int64, ok bool) {
	if t.SizedReqCalls == 0 {
		return 0, false
	}
	return t.ReqBytesSum / t.SizedReqCalls, true
}

// ErrorRate returns Errors/Calls (0 when there are no calls).
func (t *ToolUsage) ErrorRate() float64 {
	if t.Calls == 0 {
		return 0
	}
	return float64(t.Errors) / float64(t.Calls)
}

// Percentile returns an approximate latency percentile (in ms) derived from the
// fixed latency histogram. p is in [0,1]. The returned value is the upper bound
// of the bucket in which the percentile falls (overflow bucket -> last bound).
func (t *ToolUsage) Percentile(p float64) int64 {
	total := int64(0)
	for _, c := range t.LatencyBuckets {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := int64(float64(total) * p)
	if target < 1 {
		target = 1
	}
	cum := int64(0)
	for i, c := range t.LatencyBuckets {
		cum += c
		if cum >= target {
			if i < len(latencyBucketBoundsMs) {
				return latencyBucketBoundsMs[i]
			}
			return latencyBucketBoundsMs[len(latencyBucketBoundsMs)-1]
		}
	}
	return latencyBucketBoundsMs[len(latencyBucketBoundsMs)-1]
}

func (t *ToolUsage) clone() *ToolUsage {
	c := *t
	c.LatencyBuckets = make([]int64, len(t.LatencyBuckets))
	copy(c.LatencyBuckets, t.LatencyBuckets)
	return &c
}

// TimeBucket is a pre-bucketed slice of call volume over time for the timeline.
type TimeBucket struct {
	Start        time.Time `json:"start"`
	Calls        int64     `json:"calls"`
	Errors       int64     `json:"errors"`
	RespBytesSum int64     `json:"resp_bytes_sum"`
}

// UsageAggregate is the actor-owned rollup. Exported fields are JSON-serialized
// for persistence; unexported config fields are restored on construction.
type UsageAggregate struct {
	Tools     map[string]*ToolUsage `json:"tools"`
	Buckets   map[int64]*TimeBucket `json:"buckets"` // key = bucket start unix seconds
	UpdatedAt time.Time             `json:"updated_at"`
}

func newUsageAggregate() *UsageAggregate {
	return &UsageAggregate{
		Tools:   make(map[string]*ToolUsage),
		Buckets: make(map[int64]*TimeBucket),
	}
}

// tool returns the per-(server,tool) rollup, creating it on first use. It also
// defensively resizes a persisted snapshot from an older latency-bucket layout
// rather than panicking on index.
func (a *UsageAggregate) tool(server, toolName string) *ToolUsage {
	key := toolKey(server, toolName)
	tu := a.Tools[key]
	if tu == nil {
		tu = &ToolUsage{
			Server:         server,
			Tool:           toolName,
			LatencyBuckets: make([]int64, numLatencyBuckets()),
		}
		a.Tools[key] = tu
	} else if len(tu.LatencyBuckets) != numLatencyBuckets() {
		resized := make([]int64, numLatencyBuckets())
		copy(resized, tu.LatencyBuckets)
		tu.LatencyBuckets = resized
	}
	return tu
}

// Apply folds a single activity record into the aggregate. Apply is not safe
// for concurrent use; it is called only by the owning goroutine (see
// UsageStore).
//
// Two populations live in here and they are NOT the same:
//
//   - the per-tool rollup (ToolUsage) describes UPSTREAM TOOLS: calls, latency
//     percentiles, byte averages, blocked and shed attempts. Only records that
//     name an upstream tool belong in it.
//   - the timeline (TimeBucket) describes WHAT THE USER SAW. It must match the
//     glance row-for-row, or the bars under the list disagree with the list
//     above them — the single most confusing thing a dashboard can do.
//
// The glance shows tool_calls, the internal tool calls that are mcpproxy's own
// work (retrieve_tools, describe_tool, code_execution, upstream_servers …) and
// blocked policy decisions, so all three enter the timeline. Internal
// call_tool_* records are the one exclusion: they mirror a direct dispatch that
// ALREADY emitted its own tool_call record with the same request id, so
// counting them too would double every direct call.
func (a *UsageAggregate) Apply(rec *storage.ActivityRecord) {
	if rec == nil || rec.ToolName == "" {
		return
	}
	switch {
	case rec.Type == storage.ActivityTypeToolCall && rec.Status == storage.ActivityStatusRejected:
		// Spec 093: shed by a concurrency limit. Like a policy block it never
		// executed, so it must not inflate Calls, latency percentiles, byte
		// averages or the executed-call timeline.
		a.applyRejected(rec)
		return
	case rec.Type == storage.ActivityTypeToolCall:
		// folded below
	case rec.Type == storage.ActivityTypeInternalToolCall:
		a.applyInternalToolCall(rec)
		return
	case rec.Type == storage.ActivityTypePolicyDecision &&
		(rec.Status == storage.ActivityStatusBlocked || rec.Status == storage.ActivityStatusRejected):
		a.applyBlocked(rec)
		return
	default:
		return
	}

	tu := a.tool(rec.ServerName, rec.ToolName)

	tu.Calls++
	switch rec.Status {
	case "error":
		tu.Errors++
	case "blocked":
		tu.Blocked++
	}
	if rec.ResponseBytes > 0 {
		tu.RespBytesSum += int64(rec.ResponseBytes)
		tu.SizedRespCalls++
	}
	if rec.RequestBytes > 0 {
		tu.ReqBytesSum += int64(rec.RequestBytes)
		tu.SizedReqCalls++
	}
	tu.LatencyBuckets[latencyBucketIndex(rec.DurationMs)]++
	if rec.Timestamp.After(tu.LastUsed) {
		tu.LastUsed = rec.Timestamp
	}

	a.applyTimeBucket(rec)
}

// applyBlocked folds a policy-blocked attempt into the per-tool Blocked counter.
// A blocked attempt never executed the tool, so it contributes no Calls,
// latency, or bytes to the PER-TOOL rollup — it only bumps Blocked and
// LastUsed, which keeps latency percentiles and byte averages free of attempts
// that never ran.
//
// It does enter the TIMELINE, as a call and as an error. The timeline mirrors
// the glance, and in the glance a blocked attempt is a red row the user made
// and did not get: leaving it out made a run that policy refused wholesale
// render as a flat, empty histogram under a list full of failures.
func (a *UsageAggregate) applyBlocked(rec *storage.ActivityRecord) {
	tu := a.tool(rec.ServerName, rec.ToolName)
	tu.Blocked++
	if rec.Timestamp.After(tu.LastUsed) {
		tu.LastUsed = rec.Timestamp
	}
	a.countInTimeBucket(rec, true)
}

// applyInternalToolCall folds one of mcpproxy's OWN tool calls (retrieve_tools,
// describe_tool, code_execution, upstream_servers, quarantine_security …) into
// the timeline only.
//
// Timeline-only is the point: these records name a BUILT-IN, not an upstream
// tool — most carry no server at all — so admitting them to the per-tool
// rollup would invent tool rows that no upstream owns and mix mcpproxy's own
// latency into upstream percentiles. But the user sees them in the glance, and
// before this the bars under that list counted none of them: an agent whose
// traffic was mostly retrieve_tools and code_execution saw an almost empty
// histogram.
//
// call_tool_* records are skipped because the direct dispatch that produced
// them also emitted a tool_call record for the same call (see
// handleCallToolVariant, which emits both) — counting both would double it.
func (a *UsageAggregate) applyInternalToolCall(rec *storage.ActivityRecord) {
	if strings.HasPrefix(rec.ToolName, storage.InternalCallToolPrefix) {
		return
	}
	// Anything the built-in did not complete is a failed row in the glance,
	// whether it errored or a policy refused it.
	a.countInTimeBucket(rec, rec.Status != storage.ActivityStatusSuccess)
}

// applyRejected folds a concurrency-limiter shed into the per-tool Rejected
// counter (spec 093 FR-012). Same shape as applyBlocked: the tool never ran, so
// nothing but Rejected and LastUsed moves.
func (a *UsageAggregate) applyRejected(rec *storage.ActivityRecord) {
	tu := a.tool(rec.ServerName, rec.ToolName)
	tu.Rejected++
	if rec.Timestamp.After(tu.LastUsed) {
		tu.LastUsed = rec.Timestamp
	}
}

// applyTimeBucket counts an executed tool_call in the timeline, classifying it
// by its own status.
func (a *UsageAggregate) applyTimeBucket(rec *storage.ActivityRecord) {
	a.countInTimeBucket(rec, rec.Status == storage.ActivityStatusError)
}

// countInTimeBucket is applyTimeBucket with the error classification supplied
// by the caller. The populations that reach the timeline disagree about what
// counts as a failure — a policy block is one, a built-in that answered
// "blocked" is one, an upstream tool_call is only one when its status is
// "error" — so the decision belongs to the admitting path, not here.
func (a *UsageAggregate) countInTimeBucket(rec *storage.ActivityRecord, isError bool) {
	start := rec.Timestamp.UTC().Truncate(usageBucketWidth)
	k := start.Unix()
	b := a.Buckets[k]
	if b == nil {
		b = &TimeBucket{Start: start}
		a.Buckets[k] = b
	}
	b.Calls++
	if isError {
		b.Errors++
	}
	if rec.ResponseBytes > 0 {
		b.RespBytesSum += int64(rec.ResponseBytes)
	}
	a.evictOldBuckets()
}

// evictOldBuckets keeps the timeline bounded to usageMaxBuckets, dropping the
// oldest buckets first.
func (a *UsageAggregate) evictOldBuckets() {
	if len(a.Buckets) <= usageMaxBuckets {
		return
	}
	keys := make([]int64, 0, len(a.Buckets))
	for k := range a.Buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys[:len(a.Buckets)-usageMaxBuckets] {
		delete(a.Buckets, k)
	}
}

// Timeline returns the time buckets in chronological order.
func (a *UsageAggregate) Timeline() []TimeBucket {
	out := make([]TimeBucket, 0, len(a.Buckets))
	for _, b := range a.Buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// clone returns a deep copy safe to publish to readers.
func (a *UsageAggregate) clone() *UsageAggregate {
	c := &UsageAggregate{
		Tools:     make(map[string]*ToolUsage, len(a.Tools)),
		Buckets:   make(map[int64]*TimeBucket, len(a.Buckets)),
		UpdatedAt: a.UpdatedAt,
	}
	for k, tu := range a.Tools {
		c.Tools[k] = tu.clone()
	}
	for k, b := range a.Buckets {
		bc := *b
		c.Buckets[k] = &bc
	}
	return c
}

// UsageStore owns the working aggregate and publishes immutable snapshots via
// an atomic pointer (copy-on-write).
//
// Spec 069 / MCP-835: the activity write path must stay O(1) and must not block.
// Apply therefore folds the record into the working aggregate under a short
// writer lock and only marks the published snapshot stale — it does NOT clone.
// The O(tools×buckets) clone is deferred to Snapshot (publish-on-read): the
// first reader after a burst of writes materializes one fresh snapshot; the
// owning activity goroutine never pays the clone cost on the hot path. Reads
// with no pending writes are lock-free (atomic load); the A3 endpoint and the
// 30s persist flush are the only readers, so clones are rare relative to writes.
type UsageStore struct {
	mu      sync.Mutex // guards working; held for O(1) mutation, and (on read) for the clone
	working *UsageAggregate
	dirty   atomic.Bool // working has unpublished mutations since the last clone
	snap    atomic.Pointer[UsageAggregate]

	// publishes counts clone+publish operations. It is both lightweight
	// observability (publish rate) and the assertion hook for the hot-path
	// contract test (MCP-835): Apply must not publish per write.
	publishes atomic.Int64
}

func newUsageStore() *UsageStore {
	s := &UsageStore{working: newUsageAggregate()}
	// Publish an initial empty snapshot so Snapshot() is never nil and a
	// no-write read is lock-free from the start.
	s.mu.Lock()
	s.publishLocked()
	s.mu.Unlock()
	return s
}

// Apply folds a record into the working aggregate. O(1): it mutates under the
// writer lock and marks the snapshot stale, but never clones on the hot path.
func (s *UsageStore) Apply(rec *storage.ActivityRecord) {
	s.mu.Lock()
	s.working.Apply(rec)
	s.dirty.Store(true)
	s.mu.Unlock()
}

// Replace swaps in a freshly built aggregate (cold-start load or rebuild). The
// new snapshot is materialized lazily on the next read, like Apply.
func (s *UsageStore) Replace(agg *UsageAggregate) {
	s.mu.Lock()
	s.working = agg
	s.dirty.Store(true)
	s.mu.Unlock()
}

// publishLocked deep-copies the working aggregate, stamps freshness, stores it
// as the current immutable snapshot, and clears the dirty flag. Caller must
// hold s.mu.
func (s *UsageStore) publishLocked() {
	c := s.working.clone()
	c.UpdatedAt = time.Now()
	s.snap.Store(c)
	s.dirty.Store(false)
	s.publishes.Add(1)
}

// Snapshot returns the latest immutable aggregate snapshot reflecting all writes
// applied so far. When writes have occurred since the last publish it
// materializes one fresh snapshot here (the clone runs off the activity hot
// path); otherwise it is a lock-free atomic load. The returned value must be
// treated as read-only.
func (s *UsageStore) Snapshot() *UsageAggregate {
	// Fast path: nothing written since the last publish — lock-free.
	if !s.dirty.Load() {
		if snap := s.snap.Load(); snap != nil {
			return snap
		}
	}
	// Stale: materialize a fresh snapshot. Double-check under the lock so
	// concurrent readers don't each re-clone.
	s.mu.Lock()
	if s.dirty.Load() {
		s.publishLocked()
	}
	snap := s.snap.Load()
	s.mu.Unlock()
	return snap
}
