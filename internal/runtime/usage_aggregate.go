package runtime

import (
	"sort"
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

// Apply folds a single activity record into the aggregate. Non tool_call
// records and records without a tool name are ignored. Apply is not safe for
// concurrent use; it is called only by the owning goroutine (see UsageStore).
func (a *UsageAggregate) Apply(rec *storage.ActivityRecord) {
	if rec == nil || rec.Type != storage.ActivityTypeToolCall || rec.ToolName == "" {
		return
	}

	key := toolKey(rec.ServerName, rec.ToolName)
	tu := a.Tools[key]
	if tu == nil {
		tu = &ToolUsage{
			Server:         rec.ServerName,
			Tool:           rec.ToolName,
			LatencyBuckets: make([]int64, numLatencyBuckets()),
		}
		a.Tools[key] = tu
	} else if len(tu.LatencyBuckets) != numLatencyBuckets() {
		// Defensive: a persisted snapshot from an older bucket layout is
		// resized rather than panicking on index.
		resized := make([]int64, numLatencyBuckets())
		copy(resized, tu.LatencyBuckets)
		tu.LatencyBuckets = resized
	}

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

func (a *UsageAggregate) applyTimeBucket(rec *storage.ActivityRecord) {
	start := rec.Timestamp.UTC().Truncate(usageBucketWidth)
	k := start.Unix()
	b := a.Buckets[k]
	if b == nil {
		b = &TimeBucket{Start: start}
		a.Buckets[k] = b
	}
	b.Calls++
	if rec.Status == "error" {
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
// an atomic pointer (copy-on-write). Apply/Replace are invoked by the single
// owning goroutine; Snapshot is safe for any number of concurrent readers and
// never blocks.
type UsageStore struct {
	mu      sync.Mutex // serializes working mutation (uncontended single-writer)
	working *UsageAggregate
	snap    atomic.Pointer[UsageAggregate]
}

func newUsageStore() *UsageStore {
	s := &UsageStore{working: newUsageAggregate()}
	s.publishLocked()
	return s
}

// Apply folds a record into the working aggregate and republishes the snapshot.
func (s *UsageStore) Apply(rec *storage.ActivityRecord) {
	s.mu.Lock()
	s.working.Apply(rec)
	s.publishLocked()
	s.mu.Unlock()
}

// Replace swaps in a freshly built aggregate (cold-start load or rebuild) and
// publishes it as the new snapshot.
func (s *UsageStore) Replace(agg *UsageAggregate) {
	s.mu.Lock()
	s.working = agg
	s.publishLocked()
	s.mu.Unlock()
}

// publishLocked deep-copies the working aggregate, stamps freshness, and stores
// it as the current immutable snapshot. Caller must hold s.mu.
func (s *UsageStore) publishLocked() {
	c := s.working.clone()
	c.UpdatedAt = time.Now()
	s.snap.Store(c)
}

// Snapshot returns the latest immutable aggregate snapshot. Lock-free; never
// blocks. The returned value must be treated as read-only.
func (s *UsageStore) Snapshot() *UsageAggregate {
	return s.snap.Load()
}
