package runtime

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// Spec 109 SC-011, normative measurement: "GET /api/v1/attention p95 ≤ 20 ms
// at 100 servers / 1,000 tools (served from the in-memory snapshot)".
//
// GET /attention itself is O(1) against the subscriber's cached snapshot
// (attentionSubscriber.Items / httpapi's handleGetAttention) — the request
// path only filters that slice per caller scope and JSON-encodes it, both
// flat O(items) work already covered by internal/httpapi's own tests. What
// SC-011 actually guards is the computation that PRODUCES the snapshot,
// Compute, against an accidental O(servers*tools) pass creeping in (e.g.
// scanning individual tool rows instead of reading the already-aggregated
// Pending/Changed counts an AttentionServer carries). That guard belongs in
// internal/runtime (T059), since internal/httpapi cannot import it back and
// has no visibility into Compute's internals.
//
// attentionBenchP95Ceiling keeps the same wide, one-shot-CI-safe margin as
// preflightBenchPerOpCeiling (internal/httpapi/preflight_bench_test.go):
// Compute does a single append pass plus a stable sort over at most a few
// hundred items, several orders of magnitude below the 20 ms budget, so a
// crossing signals an architectural regression rather than scheduler noise.
const attentionBenchP95Ceiling = 20 * time.Millisecond

// attentionBenchRuns is how many timed Compute calls the p95 sample draws
// from. Each run is independent (Compute takes only its input; no shared
// mutable state), so run order never affects timing.
const attentionBenchRuns = 200

// attentionBenchServers and attentionBenchToolsPerServer are SC-011's fixed
// fixture shape: 100 servers * 10 tools/server = 1,000 tools.
const (
	attentionBenchServers        = 100
	attentionBenchToolsPerServer = 10
)

// buildAttentionBenchFleet returns the SC-011 fixture: 100 servers whose
// Pending/Changed counts sum to 1,000 "tools" spread across a realistic mix
// of health and quarantine states (sign-in-required, quarantined, tool
// review, connection error, missing secret, ready), plus 50 connected
// clients past the never-seen threshold — every branch Compute has.
func buildAttentionBenchFleet(now time.Time) AttentionInput {
	servers := make([]AttentionServer, 0, attentionBenchServers)
	for i := 0; i < attentionBenchServers; i++ {
		s := AttentionServer{
			Name:       fmt.Sprintf("server-%03d", i),
			Enabled:    true,
			StateSince: now.Add(-90 * time.Second),
			Health:     contracts.HealthStatus{Status: health.StatusReady},
		}
		switch i % 5 {
		case 0:
			s.Health.Status = health.StatusSignInRequired
		case 1:
			s.Quarantined = true
		case 2:
			// This server's tools split evenly between pending and changed,
			// so every fifth server's toolsPerServer tools are accounted
			// for: 20 servers * 10 tools = 200 of the 1,000; the remaining
			// 800 sit "ready" or otherwise unreviewed, matching a real fleet
			// where most tools are already approved.
			s.Pending = attentionBenchToolsPerServer / 2
			s.Changed = attentionBenchToolsPerServer / 2
		case 3:
			s.Health.Status = health.StatusError
		case 4:
			s.Health.Status = health.StatusNeedsSecret
		}
		servers = append(servers, s)
	}

	clients := make([]AttentionClient, 0, 50)
	for i := 0; i < 50; i++ {
		connectedAt := now.Add(-10 * time.Minute)
		clients = append(clients, AttentionClient{ID: fmt.Sprintf("client-%02d", i), ConnectedAt: &connectedAt})
	}

	return AttentionInput{Now: now, Servers: servers, Clients: clients}
}

// TestComputeSC011LargeFleetP95 is the SC-011 regression guard (T059): at
// 100 servers / 1,000 tools, Compute's p95 wall-clock duration must stay at
// or under the 20 ms GET /attention budget. It runs under a plain
// `go test` (not only `-bench`), so it executes in every CI lane and cannot
// be skipped by a build that never passes -bench.
func TestComputeSC011LargeFleetP95(t *testing.T) {
	in := buildAttentionBenchFleet(time.Now())

	durations := make([]time.Duration, 0, attentionBenchRuns)
	for i := 0; i < attentionBenchRuns; i++ {
		start := time.Now()
		items := Compute(in)
		durations = append(durations, time.Since(start))
		if len(items) == 0 {
			t.Fatal("fixture produced no attention items; the p95 assertion would pass vacuously")
		}
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(len(durations))*0.95)]
	if p95 > attentionBenchP95Ceiling {
		t.Errorf("Compute p95 = %v over the SC-011 %v budget at %d servers / %d tools",
			p95, attentionBenchP95Ceiling, attentionBenchServers, attentionBenchServers*attentionBenchToolsPerServer)
	}
}

// BenchmarkComputeSC011LargeFleet is the `go test -bench` companion to
// TestComputeSC011LargeFleetP95, for profiling and `-benchmem` — same
// fixture, standard testing.B per-op timing.
func BenchmarkComputeSC011LargeFleet(b *testing.B) {
	in := buildAttentionBenchFleet(time.Now())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compute(in)
	}
}
