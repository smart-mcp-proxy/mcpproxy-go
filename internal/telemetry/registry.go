package telemetry

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Surface identifies which client surface originated a request.
type Surface int

const (
	SurfaceMCP Surface = iota
	SurfaceCLI
	SurfaceWebUI
	SurfaceTray
	SurfaceUnknown
	surfaceCount
)

// String returns the JSON key for a Surface.
func (s Surface) String() string {
	switch s {
	case SurfaceMCP:
		return "mcp"
	case SurfaceCLI:
		return "cli"
	case SurfaceWebUI:
		return "webui"
	case SurfaceTray:
		return "tray"
	case SurfaceUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ParseClientSurface maps the X-MCPProxy-Client header value to a Surface enum.
// The expected format is "<surface>/<version>"; unknown prefixes and missing
// headers map to SurfaceUnknown.
func ParseClientSurface(header string) Surface {
	if header == "" {
		return SurfaceUnknown
	}
	prefix := header
	if i := strings.IndexByte(header, '/'); i >= 0 {
		prefix = header[:i]
	}
	switch strings.ToLower(prefix) {
	case "mcp":
		return SurfaceMCP
	case "cli":
		return SurfaceCLI
	case "webui":
		return SurfaceWebUI
	case "tray":
		return SurfaceTray
	default:
		return SurfaceUnknown
	}
}

// builtinToolAllowList is the fixed set of mcpproxy-owned tool names that may
// appear in the heartbeat. Names are explicitly enumerated to prevent leakage
// of upstream tool names.
var builtinToolAllowList = map[string]struct{}{
	"retrieve_tools":        {},
	"call_tool_read":        {},
	"call_tool_write":       {},
	"call_tool_destructive": {},
	"upstream_servers":      {},
	"quarantine_security":   {},
	"code_execution":        {},
}

// IsBuiltinTool reports whether the given tool name is in the fixed enum.
func IsBuiltinTool(name string) bool {
	_, ok := builtinToolAllowList[name]
	return ok
}

// DoctorCheckResult is the minimal interface the registry needs to record a
// doctor check outcome. It is satisfied by internal/doctor.CheckResult without
// importing that package (avoiding an import cycle).
type DoctorCheckResult interface {
	GetName() string
	IsPass() bool
}

// DoctorCounts holds pass/fail counts for a single doctor check.
type DoctorCounts struct {
	Pass int64 `json:"pass"`
	Fail int64 `json:"fail"`
}

// CounterRegistry aggregates Tier 2 telemetry counters in memory. All methods
// are safe for concurrent use. Counters are zeroed only by Reset(), which the
// telemetry service calls after a successful heartbeat send.
type CounterRegistry struct {
	// Atomic counters (lock-free hot path).
	surfaceCounts [surfaceCount]atomic.Int64
	upstreamTotal atomic.Int64
	// Spec 044: counts anonymity-scanner rejections. Never transmitted in
	// the heartbeat (that would defeat anonymity — the payload is
	// discarded); exposed via Snapshot for observability tests + internal
	// debugging. NOT reset on heartbeat success: this is a lifetime tally
	// of the defense-in-depth scanner firing.
	anonymityViolations atomic.Int64

	// Locked maps for variable-cardinality counters.
	mu              sync.RWMutex
	builtinCalls    map[string]int64
	restEndpoints   map[string]map[string]int64 // method+template -> status class -> count
	errorCategories map[ErrorCategory]int64
	doctorChecks    map[string]*DoctorCounts
}

// NewCounterRegistry creates an empty registry. All counters start at zero.
func NewCounterRegistry() *CounterRegistry {
	return &CounterRegistry{
		builtinCalls:    make(map[string]int64),
		restEndpoints:   make(map[string]map[string]int64),
		errorCategories: make(map[ErrorCategory]int64),
		doctorChecks:    make(map[string]*DoctorCounts),
	}
}

// RecordSurface increments the counter for the given surface.
func (r *CounterRegistry) RecordSurface(s Surface) {
	if s < 0 || s >= surfaceCount {
		s = SurfaceUnknown
	}
	r.surfaceCounts[s].Add(1)
}

// Nil-safe convenience wrappers. Many integration points may receive a nil
// registry (telemetry not initialized yet); these helpers let callers skip
// the nil check.

// RecordSurfaceOn calls reg.RecordSurface(s) if reg is non-nil.
func RecordSurfaceOn(reg *CounterRegistry, s Surface) {
	if reg == nil {
		return
	}
	reg.RecordSurface(s)
}

// RecordBuiltinToolOn calls reg.RecordBuiltinTool(name) if reg is non-nil.
func RecordBuiltinToolOn(reg *CounterRegistry, name string) {
	if reg == nil {
		return
	}
	reg.RecordBuiltinTool(name)
}

// RecordUpstreamToolOn calls reg.RecordUpstreamTool() if reg is non-nil.
func RecordUpstreamToolOn(reg *CounterRegistry) {
	if reg == nil {
		return
	}
	reg.RecordUpstreamTool()
}

// RecordRESTRequestOn calls reg.RecordRESTRequest(...) if reg is non-nil.
func RecordRESTRequestOn(reg *CounterRegistry, method, template, statusClass string) {
	if reg == nil {
		return
	}
	reg.RecordRESTRequest(method, template, statusClass)
}

// RecordErrorOn calls reg.RecordError(c) if reg is non-nil.
func RecordErrorOn(reg *CounterRegistry, c ErrorCategory) {
	if reg == nil {
		return
	}
	reg.RecordError(c)
}

// RecordBuiltinTool increments the counter for the named built-in tool.
// Unknown names (i.e., upstream tool names) are silently dropped.
func (r *CounterRegistry) RecordBuiltinTool(name string) {
	if !IsBuiltinTool(name) {
		return
	}
	r.mu.Lock()
	r.builtinCalls[name]++
	r.mu.Unlock()
}

// RecordUpstreamTool increments the upstream tool call counter. The tool name
// itself is intentionally not accepted: only an aggregate count is recorded.
func (r *CounterRegistry) RecordUpstreamTool() {
	r.upstreamTotal.Add(1)
}

// RecordAnonymityViolation increments the anonymity-violation counter (Spec
// 044). Called by the telemetry service when ScanForPII rejects a payload.
func (r *CounterRegistry) RecordAnonymityViolation() {
	r.anonymityViolations.Add(1)
}

// AnonymityViolationsTotal returns the lifetime count of anonymity-scanner
// rejections. Not transmitted; for test + internal observability only.
func (r *CounterRegistry) AnonymityViolationsTotal() int64 {
	return r.anonymityViolations.Load()
}

// RecordRESTRequest increments the counter for the given route template and
// status class. Both inputs are expected to be from a fixed enum (Chi route
// template + "2xx"/"3xx"/"4xx"/"5xx") so no sanitization is needed here.
func (r *CounterRegistry) RecordRESTRequest(method, template, statusClass string) {
	key := method + " " + template
	r.mu.Lock()
	defer r.mu.Unlock()
	inner, ok := r.restEndpoints[key]
	if !ok {
		inner = make(map[string]int64)
		r.restEndpoints[key] = inner
	}
	inner[statusClass]++
}

// RecordError increments the counter for the given error category. Unknown
// categories are silently dropped.
func (r *CounterRegistry) RecordError(c ErrorCategory) {
	if !IsValidErrorCategory(c) {
		return
	}
	r.mu.Lock()
	r.errorCategories[c]++
	r.mu.Unlock()
}

// RecordDoctorRun aggregates the structured doctor check results into the
// registry's doctor counter. Each result increments either Pass or Fail for
// its check name.
func (r *CounterRegistry) RecordDoctorRun(results []DoctorCheckResult) {
	if len(results) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, res := range results {
		name := res.GetName()
		if name == "" {
			continue
		}
		dc, ok := r.doctorChecks[name]
		if !ok {
			dc = &DoctorCounts{}
			r.doctorChecks[name] = dc
		}
		if res.IsPass() {
			dc.Pass++
		} else {
			dc.Fail++
		}
	}
}

// RegistrySnapshot is an immutable view of the registry built by Snapshot().
// It is safe to mutate the maps in a snapshot — they are copies.
type RegistrySnapshot struct {
	SurfaceCounts               map[string]int64            `json:"surface_requests"`
	BuiltinToolCalls            map[string]int64            `json:"builtin_tool_calls"`
	UpstreamToolCallCountBucket string                      `json:"upstream_tool_call_count_bucket"`
	RESTEndpointCalls           map[string]map[string]int64 `json:"rest_endpoint_calls"`
	ErrorCategoryCounts         map[string]int64            `json:"error_category_counts"`
	DoctorChecks                map[string]DoctorCounts     `json:"doctor_checks"`
}

// Snapshot returns an immutable view of all counters. The registry is NOT
// reset; call Reset() after a successful flush.
func (r *CounterRegistry) Snapshot() RegistrySnapshot {
	snap := RegistrySnapshot{
		SurfaceCounts:               make(map[string]int64, surfaceCount),
		BuiltinToolCalls:            make(map[string]int64),
		UpstreamToolCallCountBucket: bucketUpstream(r.upstreamTotal.Load()),
		RESTEndpointCalls:           make(map[string]map[string]int64),
		ErrorCategoryCounts:         make(map[string]int64),
		DoctorChecks:                make(map[string]DoctorCounts),
	}

	// Surface counts: every key is always present, even if zero.
	for s := Surface(0); s < surfaceCount; s++ {
		snap.SurfaceCounts[s.String()] = r.surfaceCounts[s].Load()
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for k, v := range r.builtinCalls {
		snap.BuiltinToolCalls[k] = v
	}
	for k, inner := range r.restEndpoints {
		copied := make(map[string]int64, len(inner))
		for sc, c := range inner {
			copied[sc] = c
		}
		snap.RESTEndpointCalls[k] = copied
	}
	for k, v := range r.errorCategories {
		snap.ErrorCategoryCounts[string(k)] = v
	}
	for k, v := range r.doctorChecks {
		snap.DoctorChecks[k] = *v
	}

	return snap
}

// Reset zeros all counters. Called only after a successful heartbeat send.
func (r *CounterRegistry) Reset() {
	for i := range r.surfaceCounts {
		r.surfaceCounts[i].Store(0)
	}
	r.upstreamTotal.Store(0)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtinCalls = make(map[string]int64)
	r.restEndpoints = make(map[string]map[string]int64)
	r.errorCategories = make(map[ErrorCategory]int64)
	r.doctorChecks = make(map[string]*DoctorCounts)
}

// bucketUpstream maps an upstream tool call count to its log bucket label.
func bucketUpstream(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n <= 10:
		return "1-10"
	case n <= 100:
		return "11-100"
	case n <= 1000:
		return "101-1000"
	default:
		return "1000+"
	}
}

// SortedOAuthProviderTypes returns a sorted, deduplicated list. Helper used
// by feature_flags.go but defined here to avoid an extra file.
func SortedOAuthProviderTypes(types []string) []string {
	if len(types) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(types))
	out := make([]string, 0, len(types))
	for _, t := range types {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
